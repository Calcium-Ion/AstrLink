package clientconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
)

// Writing and removing client configs, one place or a batch, and keeping
// the ones the gateway wrote pointed at its current port.

// ReplacedKey is a value AstrLink did not write that a write replaces or
// removes. Both values show as the change preview does: a credential by
// its ends only.
type ReplacedKey struct {
	// Path is the file holding it.
	Path string `json:"path"`
	Key  string `json:"key"`
	// Current is the value now; nil when it is not one line of text.
	Current *string `json:"current"`
	// Next is the value the write puts there; nil when the write removes
	// the key or the value is not one line of text.
	Next *string `json:"next"`
	// Removed reports whether the write removes the key.
	Removed bool `json:"removed"`
}

// Confirmation is what a client's write or removal asks the user to
// confirm.
type Confirmation struct {
	Client Client  `json:"client"`
	WSL    *string `json:"wsl"`
	// Target is one of the client's targets; nil for a client without.
	Target *string `json:"target"`
	Dir    string  `json:"dir"`
	// Remove is set when AstrLink's config is removed.
	Remove bool          `json:"remove"`
	Keys   []ReplacedKey `json:"keys"`
	// Gateway is the base URL of another AstrLink the config connects to
	// now.
	Gateway *string `json:"gateway"`
	// TokenID is another token of the gateway the config uses now.
	TokenID *string `json:"token_id"`
}

func (c *Confirmation) needed() bool {
	return c.Remove || len(c.Keys) > 0 || c.Gateway != nil || c.TokenID != nil
}

// Action is what an item does to its place.
type Action string

const (
	ActionWrite  Action = "write"
	ActionRemove Action = "remove"
)

func (a *Action) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	switch Action(name) {
	case ActionWrite, ActionRemove:
		*a = Action(name)
		return nil
	}
	return fmt.Errorf("unknown variant `%s`, expected `write` or `remove`", name)
}

// ApplyItem is one place to write or remove, in the desktop user's home or
// a WSL one.
type ApplyItem struct {
	Client Client  `json:"client"`
	WSL    *string `json:"wsl"`
	// Target is one of the client's targets; nil for a client without.
	Target *string `json:"target"`
	Action Action  `json:"action"`
	Models *Models `json:"models"`
}

// UnmarshalJSON reads an item as serde does: no unknown field, and the
// client and action required.
func (i *ApplyItem) UnmarshalJSON(data []byte) error {
	type item ApplyItem
	var raw struct {
		item
		Client *Client `json:"client"`
		Action *Action `json:"action"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	switch {
	case raw.Client == nil:
		return errors.New("missing field `client`")
	case raw.Action == nil:
		return errors.New("missing field `action`")
	}
	*i = ApplyItem(raw.item)
	i.Client, i.Action = *raw.Client, *raw.Action
	return nil
}

// ItemResult is how one item of a batch went.
type ItemResult struct {
	Client Client  `json:"client"`
	WSL    *string `json:"wsl"`
	// Target is one of the client's targets; nil for a client without.
	Target *string    `json:"target"`
	Action Action     `json:"action"`
	OK     bool       `json:"ok"`
	Error  *ItemError `json:"error"`
}

// BatchOutcome is what a batch did: nothing, while Items need the user's
// confirmation, or one result per item, in order.
type BatchOutcome struct {
	Items   []Confirmation
	Results []ItemResult
}

func (b BatchOutcome) MarshalJSON() ([]byte, error) {
	if b.Results == nil {
		return json.Marshal(struct {
			Status string         `json:"status"`
			Items  []Confirmation `json:"items"`
		}{"needs_confirmation", b.Items})
	}
	return json.Marshal(struct {
		Status  string       `json:"status"`
		Results []ItemResult `json:"results"`
	}{"done", b.Results})
}

// optional is text, nil when empty.
func optional(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

// Job is what one write in a batch is made of.
type Job struct {
	// Home is nil once the WSL distribution is gone.
	Home      *agents.Home
	Place     Place
	WSL       *string
	Action    Action
	Selection *Selection
	Details   Catalog
}

// Secret is the token a batch writes, revealed once.
type Secret struct {
	ID, Token string
}

// HomeOf is the home an item names: the desktop user's for no WSL
// distribution; nil once the distribution is gone.
func HomeOf(homes *agents.Homes, wsl *string) *agents.Home {
	name := ""
	if wsl != nil {
		if *wsl == "" {
			return nil
		}
		name = *wsl
	}
	home, ok := homes.Get(name)
	if !ok {
		return nil
	}
	return &home
}

// Apply writes and removes each item. Without confirmed, nothing is
// written while any item needs the user's confirmation. Items are written
// one by one; one failing leaves the others written.
func Apply(ctx context.Context, gateway Gateway, homes *agents.Homes, tokenID *string, inferenceURL string, items []ApplyItem, confirmed bool) (BatchOutcome, error) {
	if len(items) == 0 || len(items) > 64 {
		return BatchOutcome{}, errors.New("invalid client list")
	}
	places := make([]Place, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		place, err := NewPlace(item.Client, item.Target)
		if err != nil {
			return BatchOutcome{}, err
		}
		places[i] = place
		key := place.Key() + "\x00"
		if item.WSL != nil {
			key += "\x00" + strings.ToLower(*item.WSL)
		}
		if seen[key] {
			return BatchOutcome{}, errors.New("a client is listed twice")
		}
		seen[key] = true
	}
	origin, err := LocalOrigin(inferenceURL)
	if err != nil {
		return BatchOutcome{}, err
	}
	selections := make([]*Selection, len(items))
	writes := false
	for i, item := range items {
		if item.Action != ActionWrite {
			continue
		}
		if item.Models == nil {
			return BatchOutcome{}, errors.New("models are required")
		}
		if selections[i], err = Select(item.Models, item.Client); err != nil {
			return BatchOutcome{}, err
		}
		writes = true
	}
	var secret *Secret
	var tokens Tokens
	if writes {
		if tokenID == nil {
			return BatchOutcome{}, errors.New("access token is required")
		}
		token, err := RevealAccessToken(ctx, gateway, *tokenID, inferenceURL)
		if err != nil {
			return BatchOutcome{}, err
		}
		secret = &Secret{*tokenID, token}
		if tokens, err = TokensOf(ctx, gateway); err != nil {
			return BatchOutcome{}, err
		}
	}
	known := Catalog{}
	for i, item := range items {
		if selections[i] != nil && item.Client.ListsModels() {
			officialDetails(ctx, gateway, selections[i].List, known)
		}
	}
	jobs := make([]Job, len(items))
	for i, item := range items {
		jobs[i] = Job{Home: HomeOf(homes, item.WSL), Place: places[i], WSL: item.WSL, Action: item.Action, Selection: selections[i], Details: Catalog{}}
		if selections[i] != nil {
			jobs[i].Details = catalogFor(selections[i], known)
		}
	}
	return Run(jobs, secret, origin, tokens, confirmed), nil
}

func unavailable(wsl *string) *ItemError {
	name := ""
	if wsl != nil {
		name = *wsl
	}
	return &ItemError{Message: fmt.Sprintf("WSL distribution %s is no longer available", name)}
}

func (j *Job) connection(secret *Secret, origin string) *Connection {
	if j.Selection == nil || secret == nil {
		return nil
	}
	return j.Selection.Connection(j.Place.Target, secret.ID, secret.Token, origin)
}

// Run is Apply once the gateway was asked.
func Run(jobs []Job, secret *Secret, origin string, tokens Tokens, confirmed bool) BatchOutcome {
	if !confirmed {
		var confirmations []Confirmation
		for _, job := range jobs {
			if job.Home == nil {
				continue
			}
			var confirmation *Confirmation
			var err error
			switch connection := job.connection(secret, origin); {
			case job.Action == ActionRemove:
				confirmation, err = removalCheck(*job.Home, job.Place)
			case connection != nil:
				confirmation, err = check(*job.Home, job.Place, connection, job.Details, tokens)
			default:
				continue
			}
			// A client that cannot be read reports it once written.
			if err == nil && confirmation != nil {
				confirmations = append(confirmations, *confirmation)
			}
		}
		if len(confirmations) > 0 {
			return BatchOutcome{Items: confirmations}
		}
	}
	results := []ItemResult{}
	for _, job := range jobs {
		var err *ItemError
		connection := job.connection(secret, origin)
		switch {
		case job.Home == nil:
			err = unavailable(job.WSL)
		case job.Action == ActionRemove:
			err = Remove(*job.Home, job.Place)
		case connection != nil:
			if tokens == nil {
				tokens = Tokens{}
			}
			var applied bool
			applied, _, err = WriteWith(*job.Home, job.Place, connection, job.Details, tokens, confirmed)
			if err == nil && !applied {
				err = &ItemError{Message: "the config changed since it was checked; write again"}
			}
		default:
			err = &ItemError{Message: "access token is required"}
		}
		results = append(results, ItemResult{
			Client: job.Place.Client,
			WSL:    job.WSL,
			Target: optional(job.Place.Target),
			Action: job.Action,
			OK:     err == nil,
			Error:  err,
		})
	}
	return BatchOutcome{Results: results}
}

// removalCheck asks first whenever AstrLink has a config to remove.
func removalCheck(home agents.Home, place Place) (*Confirmation, error) {
	defer agents.LockHostFiles()()
	records, err := ReadRecords(home)
	if err != nil {
		return nil, err
	}
	if records.Get(place) == nil {
		return nil, nil
	}
	return &Confirmation{
		Client: place.Client,
		WSL:    optional(home.Distribution()),
		Target: optional(place.Target),
		Dir:    place.Client.Dir(home, place.Target),
		Remove: true,
		Keys:   []ReplacedKey{},
	}, nil
}

// check is what writing place would ask to confirm, if anything.
func check(home agents.Home, place Place, connection *Connection, details Catalog, tokens Tokens) (*Confirmation, error) {
	defer agents.LockHostFiles()()
	records, err := ReadRecords(home)
	if err != nil {
		return nil, err
	}
	paths := place.Client.ConfigPaths(home, place.Target, records.Get(place))
	existing, err := ReadFiles(place.Client, paths)
	if err != nil {
		return nil, err
	}
	confirmation, itemErr := confirm(home, place, records, paths, existing, connection, details, tokens)
	if itemErr != nil {
		return nil, itemErr
	}
	if !confirmation.needed() {
		return nil, nil
	}
	return confirmation, nil
}

// confirm is the values a write replaces, and whether it takes the client
// from another token or another AstrLink. tokens is nil when unknown.
func confirm(home agents.Home, place Place, records *Records, paths []string, existing []*string, connection *Connection, details Catalog, tokens Tokens) (*Confirmation, *ItemError) {
	client := place.Client
	record := records.Get(place)
	plan, err := PlanWrite(client, existing, connection, details, record)
	if err != nil {
		return nil, err.At(paths)
	}
	config, err := ParseConfig(client, existing)
	if err != nil {
		return nil, err.At(paths)
	}
	state := PlaceState(records, place, existing, connection.Origin, tokens)
	out := &Confirmation{
		Client: client,
		WSL:    optional(home.Distribution()),
		Target: optional(place.Target),
		Dir:    client.Dir(home, place.Target),
		Keys:   []ReplacedKey{},
	}
	if record != nil && state == Elsewhere {
		out.Gateway = Some(record.BaseURL)
	}
	if record != nil && record.TokenID != connection.TokenID && (state == Configured || state == Outdated) && tokens != nil && tokens.Own(record) {
		out.TokenID = Some(record.TokenID)
	}
	desired := client.DesiredKeys(connection, details)
	for _, key := range plan.Conflicts {
		replaced := ReplacedKey{Path: paths[config.Index(key)], Key: key, Removed: true}
		if value, ok, _ := config.Get(key); ok {
			replaced.Current = DisplayedValue(key, value)
		}
		if at := slices.IndexFunc(desired, func(setting Setting) bool { return setting.Key == key }); at >= 0 {
			replaced.Next = DisplayedValue(key, desired[at].Value)
			replaced.Removed = false
		}
		out.Keys = append(out.Keys, replaced)
	}
	return out, nil
}

// ReadFiles reads each of client's files, nil for a missing one.
func ReadFiles(client Client, paths []string) ([]*string, error) {
	files := make([]*string, len(paths))
	for i, file := range client.Files() {
		if i >= len(paths) {
			break
		}
		text, err := FileRead(file, paths[i])
		if err != nil {
			return nil, err
		}
		files[i] = text
	}
	return files, nil
}

func expected(text *string) *agents.Expected {
	if text == nil {
		return agents.ExpectText("", false)
	}
	return agents.ExpectText(*text, true)
}

// writeFiles writes each file whose contents changed. When one fails, the
// files already written get their old contents back.
func writeFiles(client Client, paths []string, existing []*string, contents []string) error {
	files := client.Files()
	var done []int
	for i, path := range paths {
		before, after := existing[i], optional(contents[i])
		if before == nil && after == nil || before != nil && *before == contents[i] {
			continue
		}
		if err := FileWrite(files[i], path, before, after, true); err != nil {
			for j := len(done) - 1; j >= 0; j-- {
				k := done[j]
				_ = FileWrite(files[k], paths[k], optional(contents[k]), existing[k], true)
			}
			return err
		}
		done = append(done, i)
	}
	return nil
}

// WriteWith writes connection to place. tokens are the gateway's, so a
// config another AstrLink or another token wrote moves here only once the
// user confirms (replace). details is what a list entry for each model
// says besides its id. It reports false, with what to confirm, when it
// wrote nothing.
func WriteWith(home agents.Home, place Place, connection *Connection, details Catalog, tokens Tokens, replace bool) (bool, *Confirmation, *ItemError) {
	client := place.Client
	if !client.Detected(home, place.Target) {
		return false, nil, &ItemError{Message: "this client is not installed for the current user"}
	}
	if client.Running(home, place.Target) {
		return false, nil, openError(client)
	}
	defer agents.LockHostFiles()()
	records, err := ReadRecords(home)
	if err != nil {
		return false, nil, itemError(err)
	}
	paths := client.ConfigPaths(home, place.Target, records.Get(place))
	existing, err := ReadFiles(client, paths)
	if err != nil {
		return false, nil, itemError(err)
	}
	confirmation, itemErr := confirm(home, place, records, paths, existing, connection, details, tokens)
	if itemErr != nil {
		return false, nil, itemErr
	}
	if confirmation.needed() && !replace {
		return false, confirmation, nil
	}
	plan, planErr := PlanWrite(client, existing, connection, details, records.Get(place))
	if planErr != nil {
		return false, nil, planErr.At(paths)
	}
	if file, ok := client.RecordedFile(paths); ok {
		plan.Record.File = &file
	}
	previous := records.Set(place, plan.Record)
	if err := WriteRecords(home, records); err != nil {
		return false, nil, itemError(err)
	}
	if err := writeFiles(client, paths, existing, plan.Contents); err != nil {
		records.Set(place, previous)
		_ = WriteRecords(home, records)
		return false, nil, itemError(err)
	}
	return true, nil, nil
}

func openError(client Client) *ItemError {
	return &ItemError{Message: client.Name() + " is open; quit it and try again"}
}

// Remove removes what AstrLink wrote and puts back the values it replaced.
func Remove(home agents.Home, place Place) *ItemError {
	defer agents.LockHostFiles()()
	records, err := ReadRecords(home)
	if err != nil {
		return itemError(err)
	}
	record := records.Get(place)
	if record == nil {
		return nil
	}
	record = record.Clone()
	if place.Client.Running(home, place.Target) {
		return openError(place.Client)
	}
	paths := place.Client.ConfigPaths(home, place.Target, record)
	existing, err := ReadFiles(place.Client, paths)
	if err != nil {
		return itemError(err)
	}
	removals, planErr := PlanRemove(place.Client, existing, record)
	if planErr != nil {
		return planErr.At(paths)
	}
	files := place.Client.Files()
	for i, removal := range removals {
		var err error
		switch removal.Kind {
		case RemovalWrite:
			err = FileWrite(files[i], paths[i], existing[i], &removal.Contents, FileSecret(files[i]))
		case RemovalDelete:
			err = FileWrite(files[i], paths[i], existing[i], nil, FileSecret(files[i]))
		}
		if err != nil {
			return itemError(err)
		}
	}
	records.Set(place, nil)
	if err := WriteRecords(home, records); err != nil {
		return itemError(err)
	}
	return nil
}

// Sync moves every untouched config the gateway wrote with one of its
// tokens to its current address. Another AstrLink's configs stay pointed
// at it.
func Sync(home agents.Home, inferenceURL string, tokens Tokens) error {
	origin, err := LocalOrigin(inferenceURL)
	if err != nil {
		return err
	}
	defer agents.LockHostFiles()()
	records, err := ReadRecords(home)
	if err != nil {
		return err
	}
	var errs []string
	for _, place := range records.Places() {
		record := records.Get(place)
		if record == nil || !tokens.Own(record) {
			continue
		}
		record = record.Clone()
		client := place.Client
		if client.Running(home, place.Target) {
			errs = append(errs, openError(client).Message)
			continue
		}
		paths := client.ConfigPaths(home, place.Target, record)
		if err := syncPlace(home, records, place, record, paths, origin); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func syncPlace(home agents.Home, records *Records, place Place, record *Record, paths []string, origin string) error {
	existing, err := ReadFiles(place.Client, paths)
	if err != nil {
		return err
	}
	contents, next, planErr := PlanSync(place.Client, existing, record, origin)
	if planErr != nil {
		return planErr.At(paths)
	}
	if next == nil {
		return nil
	}
	records.Set(place, next)
	if err := WriteRecords(home, records); err != nil {
		return err
	}
	if err := writeFiles(place.Client, paths, existing, contents); err != nil {
		records.Set(place, record.Clone())
		_ = WriteRecords(home, records)
		return err
	}
	return nil
}
