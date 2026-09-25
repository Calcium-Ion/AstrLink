package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const billingTimeLayout = "2006-01-02T15:04:05.000000000Z"

func billingTime(t time.Time) string { return t.UTC().Format(billingTimeLayout) }
func (s *Store) PricingCatalog(ctx context.Context) (pricing.Catalog, error) {
	c := pricing.Catalog{Prices: []pricing.Price{}, Warnings: []string{}}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT document_json FROM pricing_versions ORDER BY activated_at DESC LIMIT 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err
}
func (s *Store) SavePricingCatalog(ctx context.Context, c pricing.Catalog) (err error) {
	if c.Version == "" || c.ActivatedAt.IsZero() || len(c.Prices) == 0 {
		return fmt.Errorf("invalid catalog")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO pricing_versions VALUES(?,?,?)`, c.Version, billingTime(c.ActivatedAt), string(raw)); err != nil {
		return err
	}
	for _, p := range c.Prices {
		if pricing.Providers[p.Provider] == "" || pricing.ValidateExpression(p.Expression) != nil {
			return fmt.Errorf("invalid official price")
		}
		raw, err = json.Marshal(p)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO pricing_rates VALUES(?,?,?,?)`, c.Version, p.Provider, p.Model, string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) PricingConfig(ctx context.Context, id contract.ServiceID) (pricing.Config, error) {
	service, err := s.GetService(ctx, id)
	if err != nil {
		return pricing.Config{}, err
	}
	c := pricing.DefaultConfig(service.Service.Kind)
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT document_json FROM pricing_configs WHERE service_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err
}
func (s *Store) SavePricingConfig(ctx context.Context, id contract.ServiceID, c pricing.Config) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	if _, err := s.GetService(ctx, id); err != nil {
		return err
	}
	if c.Bindings == nil {
		c.Bindings = map[string]pricing.Binding{}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO pricing_configs VALUES(?,?) ON CONFLICT(service_id) DO UPDATE SET document_json=excluded.document_json`, id, string(raw))
	return err
}

// recordBilling shares the request-record transaction: a log commit and its
// accounting checkpoint succeed together. The key follows the logical root and
// attempt number when a retry moves the earlier attempt into a child record.
func recordBilling(ctx context.Context, tx *sql.Tx, r contract.RequestRecord, revalue bool) error {
	if r.ServiceID == nil || r.Status == contract.RequestStatusBlocked {
		return nil
	}
	if r.AttemptIndex < 1 && !revalue {
		return nil
	}
	root := r.ID
	if r.ParentRequestID != nil {
		root = *r.ParentRequestID
	}
	attempt := max(1, r.AttemptIndex)
	model := ""
	if r.RequestedModel != nil {
		model = *r.RequestedModel
	}
	if r.Recovery != nil && r.Recovery.UpstreamModel != "" {
		model = r.Recovery.UpstreamModel
	}
	if revalue && r.LocalAccessTokenID != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE billing_ledger
SET local_access_token_id = ?
WHERE root_id = ? AND attempt = ? AND local_access_token_id IS NULL`,
			string(*r.LocalAccessTokenID), root, attempt); err != nil {
			return fmt.Errorf("backfill billing access token: %w", err)
		}
	}
	var serviceJSON string
	err := tx.QueryRowContext(ctx, `SELECT document_json FROM services WHERE id=?`, *r.ServiceID).Scan(&serviceJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var service contract.Service
	if err = json.Unmarshal([]byte(serviceJSON), &service); err != nil {
		return err
	}
	accountKey := pricing.AccountKey(service)
	var version, priceJSON, amount, reason, existingService, existingAccount string
	var terminal int
	err = tx.QueryRowContext(ctx, `SELECT price_version,COALESCE(price_json,''),amount_usd,reason,terminal,service_id,account_key FROM billing_ledger WHERE root_id=? AND attempt=?`, root, attempt).Scan(&version, &priceJSON, &amount, &reason, &terminal, &existingService, &existingAccount)
	exists := err == nil
	if exists {
		accountKey = existingAccount
	} else if revalue && service.Subscription != nil {
		// Legacy request records do not contain the authenticated provider account.
		// Keep those amounts in service-wide history, never assign them to today's login.
		accountKey = "legacy/" + string(service.ID)
	}

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if exists && (terminal == 1 && !revalue || revalue && reason == "priced") {
		return nil
	}
	if exists && existingService != string(*r.ServiceID) {
		return fmt.Errorf("billing attempt changed service")
	}
	if !exists || revalue {
		c := pricing.DefaultConfig(service.Kind)
		var raw string
		err = tx.QueryRowContext(ctx, `SELECT document_json FROM pricing_configs WHERE service_id=?`, service.ID).Scan(&raw)
		if err == nil {
			if err = json.Unmarshal([]byte(raw), &c); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		cutoff := billingTime(r.StartedAt)
		if revalue {
			cutoff = billingTime(time.Now())
		}
		version = ""
		priceJSON = ""
		err = tx.QueryRowContext(ctx, `SELECT version FROM pricing_versions WHERE activated_at<=? ORDER BY activated_at DESC LIMIT 1`, cutoff).Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		b, ok := c.Bindings[model]
		if !ok {
			b = pricing.Binding{Provider: c.Provider, Model: model}
		}
		rows, err := tx.QueryContext(ctx, `SELECT document_json FROM pricing_rates WHERE version=? AND model=? AND (?='' OR provider=?) LIMIT 2`, version, b.Model, b.Provider, b.Provider)
		if err != nil {
			return err
		}
		matches := []string{}
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			matches = append(matches, raw)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(matches) == 1 {
			priceJSON = matches[0]
		}
	}
	terminal = 0
	amount = "0.000000000"
	reason = "pending"
	tier := ""
	usageJSON, err := json.Marshal(r.Usage)
	if err != nil {
		return err
	}
	if r.Status != contract.RequestStatusPending {
		terminal = 1
		reason = "missing_price"
		if priceJSON != "" {
			var price pricing.Price
			if err = json.Unmarshal([]byte(priceJSON), &price); err != nil {
				return err
			}
			value, e := pricing.Evaluate(price.Expression, r.Usage, r.StartedAt)
			if e != nil {
				reason = e.Error()
			} else {
				amount = value.AmountUSD
				tier = value.Tier
				reason = "priced"
			}
		}
	}
	var localAccessTokenID any
	if r.LocalAccessTokenID != nil {
		localAccessTokenID = string(*r.LocalAccessTokenID)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO billing_ledger(root_id,attempt,service_id,account_key,model,started_at,terminal,usage_json,price_json,price_version,amount_usd,tier,reason,revalued,local_access_token_id)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(root_id,attempt) DO UPDATE SET terminal=excluded.terminal,usage_json=excluded.usage_json,price_json=excluded.price_json,price_version=excluded.price_version,amount_usd=excluded.amount_usd,tier=excluded.tier,reason=excluded.reason,revalued=excluded.revalued,local_access_token_id=COALESCE(billing_ledger.local_access_token_id, excluded.local_access_token_id)`, root, attempt, service.ID, accountKey, model, billingTime(r.StartedAt), terminal, string(usageJSON), priceJSON, version, amount, tier, reason, revalue, localAccessTokenID)
	return err
}

// PriceUnpriced fills missing prices from new catalogs and retries audio
// breakdown failures with the original price snapshot after evaluator fixes.
// It does not import legacy logs or rewrite amounts that were already priced.
func (s *Store) PriceUnpriced(ctx context.Context) (int, error) {
	catalog, err := s.PricingCatalog(ctx)
	if err != nil || catalog.Version == "" {
		return 0, err
	}
	processed := 0
	var lastRoot string
	lastAttempt := 0
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT b.root_id,b.attempt,b.service_id,b.started_at,b.model,b.usage_json,COALESCE(b.price_json,''),b.reason
FROM billing_ledger b JOIN services s ON s.id=b.service_id
WHERE b.terminal=1 AND ((b.reason='missing_price' AND b.price_version<>?)
OR b.reason IN ('missing_audio_usage','missing_audio_cache_partition'))
AND (b.root_id>? OR (b.root_id=? AND b.attempt>?))
ORDER BY b.root_id,b.attempt LIMIT 100`, catalog.Version, lastRoot, lastRoot, lastAttempt)
		if err != nil {
			return processed, err
		}
		type pendingRecord struct {
			contract.RequestRecord
			priceJSON, usageJSON, reason string
		}
		records := []pendingRecord{}
		for rows.Next() {
			var r pendingRecord
			var id contract.ServiceID
			var started, model string
			if err = rows.Scan(&r.ID, &r.AttemptIndex, &id, &started, &model, &r.usageJSON, &r.priceJSON, &r.reason); err != nil {
				rows.Close()
				return processed, err
			}
			r.ServiceID, r.RequestedModel = &id, &model
			r.Status = contract.RequestStatusSucceeded
			if r.StartedAt, err = time.Parse(time.RFC3339Nano, started); err != nil {
				rows.Close()
				return processed, err
			}
			if err = json.Unmarshal([]byte(r.usageJSON), &r.Usage); err != nil {
				rows.Close()
				return processed, err
			}
			records = append(records, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(records) == 0 {
			return processed, err
		}
		for _, r := range records {
			lastRoot, lastAttempt = string(r.ID), r.AttemptIndex
			if r.reason != "missing_price" {
				var price pricing.Price
				if err = json.Unmarshal([]byte(r.priceJSON), &price); err != nil {
					return processed, err
				}
				value, evaluationErr := pricing.Evaluate(price.Expression, r.Usage, r.StartedAt)
				if evaluationErr != nil {
					continue // A genuinely unknown, price-sensitive split stays unpriced.
				}
				result, err := s.db.ExecContext(ctx, `UPDATE billing_ledger SET amount_usd=?,tier=?,reason='priced',revalued=1
WHERE root_id=? AND attempt=? AND terminal=1 AND reason=? AND price_json=? AND usage_json=?`, value.AmountUSD, value.Tier, r.ID, r.AttemptIndex, r.reason, r.priceJSON, r.usageJSON)
				if err != nil {
					return processed, err
				}
				n, err := result.RowsAffected()
				if err != nil {
					return processed, err
				}
				processed += int(n)
				continue
			}
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return processed, err
			}
			if err = recordBilling(ctx, tx, r.RequestRecord, true); err != nil {
				tx.Rollback()
				return processed, err
			}
			if err = tx.Commit(); err != nil {
				return processed, err
			}
			processed++
		}
	}
}

// Backfill is explicit: old/missing prices are valued using today's selected
// official catalog. Previously priced calls never change on sync or backfill.
func (s *Store) BackfillPricing(ctx context.Context, id contract.ServiceID) (int, error) {
	if _, err := s.GetService(ctx, id); err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,parent_request_id,attempt_index,started_at,status,requested_model,recovery_json,usage_json,local_access_token_id FROM request_records WHERE service_id=? AND status NOT IN ('pending','blocked')`, id)
	if err != nil {
		return 0, err
	}
	records := []contract.RequestRecord{}
	for rows.Next() {
		var r contract.RequestRecord
		var parent, model, recovery, usage, localAccessTokenID sql.NullString
		var started string
		if err = rows.Scan(&r.ID, &parent, &r.AttemptIndex, &started, &r.Status, &model, &recovery, &usage, &localAccessTokenID); err != nil {
			rows.Close()
			return 0, err
		}
		r.ServiceID = &id
		if localAccessTokenID.Valid {
			tokenID := contract.AccessTokenID(localAccessTokenID.String)
			r.LocalAccessTokenID = &tokenID
		}
		r.StartedAt, err = time.Parse(time.RFC3339Nano, started)
		if err != nil {
			rows.Close()
			return 0, err
		}
		if parent.Valid {
			p := contract.RequestID(parent.String)
			r.ParentRequestID = &p
		}
		if model.Valid {
			v := model.String
			r.RequestedModel = &v
		}
		if recovery.Valid {
			if err = json.Unmarshal([]byte(recovery.String), &r.Recovery); err != nil {
				rows.Close()
				return 0, err
			}
		}
		if usage.Valid {
			if err = json.Unmarshal([]byte(usage.String), &r.Usage); err != nil {
				rows.Close()
				return 0, err
			}
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	// Include retained ledger entries after their detailed request logs were pruned.
	ledger, err := s.db.QueryContext(ctx, `SELECT root_id,attempt,started_at,model,usage_json FROM billing_ledger WHERE service_id=? AND terminal=1 AND reason<>'priced'`, id)
	if err != nil {
		return 0, err
	}
	for ledger.Next() {
		var r contract.RequestRecord
		var started, model, usage string
		if err = ledger.Scan(&r.ID, &r.AttemptIndex, &started, &model, &usage); err != nil {
			ledger.Close()
			return 0, err
		}
		r.ServiceID = &id
		r.RequestedModel = &model
		r.Status = contract.RequestStatusSucceeded
		r.StartedAt, err = time.Parse(time.RFC3339Nano, started)
		if err != nil {
			ledger.Close()
			return 0, err
		}
		if err = json.Unmarshal([]byte(usage), &r.Usage); err != nil {
			ledger.Close()
			return 0, err
		}
		records = append(records, r)
	}
	err = ledger.Err()
	ledger.Close()
	if err != nil {
		return 0, err
	}
	for _, r := range records {
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return 0, e
		}
		if e = recordBilling(ctx, tx, r, true); e != nil {
			tx.Rollback()
			return 0, e
		}
		if e = tx.Commit(); e != nil {
			return 0, e
		}
	}
	return len(records), nil
}

func (s *Store) BillingSummary(ctx context.Context, id contract.ServiceID, account string, from, to time.Time, options pricing.BillingSummaryOptions) (pricing.Summary, error) {
	result := pricing.Summary{From: from, To: to, Amounts: pricing.Amounts{AmountUSD: "0.000000000"}, ByModel: []pricing.Group{}, ByToken: []pricing.TokenGroup{}}
	if from.IsZero() || !to.After(from) || to.Sub(from) > 367*24*time.Hour {
		return result, fmt.Errorf("%w: invalid billing range", storage.ErrInvalidArgument)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT root_id,local_access_token_id,model,COALESCE(json_extract(NULLIF(price_json,''),'$.provider'),''),amount_usd,reason,revalued FROM billing_ledger WHERE started_at>=? AND started_at<? AND (?='' OR service_id=?) AND (?='' OR account_key=?)`, billingTime(from), billingTime(to), id, id, account, account)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	totals := new(big.Rat)
	groups := map[string]*pricing.Group{}
	groupAmounts := map[string]*big.Rat{}
	roots := map[string]bool{}
	groupRoots := map[string]map[string]bool{}
	currentTokenIDs := map[string]struct{}{}
	if options.IncludeTokenBreakdown {
		currentTokenIDs, err = s.currentAccessTokenIDs(ctx)
		if err != nil {
			return result, err
		}
	}
	tokenGroups := map[string]*pricing.TokenGroup{}
	tokenAmounts := map[string]*big.Rat{}
	tokenRoots := map[string]map[string]bool{}
	for rows.Next() {
		var root, model, provider, amount, reason string
		var localAccessTokenID sql.NullString
		var revalued bool
		if err = rows.Scan(&root, &localAccessTokenID, &model, &provider, &amount, &reason, &revalued); err != nil {
			return result, err
		}
		key := provider + "/" + model
		if groups[key] == nil {
			groups[key] = &pricing.Group{Model: model, Provider: provider}
			groupAmounts[key] = new(big.Rat)
			groupRoots[key] = map[string]bool{}
		}
		var tokenGroup *pricing.TokenGroup
		if options.IncludeTokenBreakdown && localAccessTokenID.Valid {
			if _, ok := currentTokenIDs[localAccessTokenID.String]; ok {
				if tokenGroups[localAccessTokenID.String] == nil {
					tokenGroups[localAccessTokenID.String] = &pricing.TokenGroup{TokenID: localAccessTokenID.String}
					tokenAmounts[localAccessTokenID.String] = new(big.Rat)
					tokenRoots[localAccessTokenID.String] = map[string]bool{}
				}
				tokenGroup = tokenGroups[localAccessTokenID.String]
				tokenRoots[localAccessTokenID.String][root] = true
			}
		}
		roots[root] = true
		groupRoots[key][root] = true
		amounts := []*pricing.Amounts{&result.Amounts, &groups[key].Amounts}
		if tokenGroup != nil {
			amounts = append(amounts, &tokenGroup.Amounts)
		}
		for _, a := range amounts {
			switch reason {
			case "priced":
				a.Priced++
			case "pending":
				a.Pending++
			default:
				a.Unpriced++
			}
			if revalued {
				a.Revalued++
			}
		}
		if reason == "priced" {
			v, ok := new(big.Rat).SetString(amount)
			if !ok {
				return result, fmt.Errorf("invalid stored amount")
			}
			totals.Add(totals, v)
			groupAmounts[key].Add(groupAmounts[key], v)
			if tokenGroup != nil {
				tokenAmounts[tokenGroup.TokenID].Add(tokenAmounts[tokenGroup.TokenID], v)
			}
		}
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	result.AmountUSD = totals.FloatString(9)
	result.Requests = int64(len(roots))
	for key, g := range groups {
		g.AmountUSD = groupAmounts[key].FloatString(9)
		g.Requests = int64(len(groupRoots[key]))
		result.ByModel = append(result.ByModel, *g)
	}
	if options.IncludeTokenBreakdown {
		for tokenID, group := range tokenGroups {
			group.AmountUSD = tokenAmounts[tokenID].FloatString(9)
			group.Requests = int64(len(tokenRoots[tokenID]))
			result.ByToken = append(result.ByToken, *group)
		}
		slices.SortFunc(result.ByToken, func(a, b pricing.TokenGroup) int {
			av, _ := new(big.Rat).SetString(a.AmountUSD)
			bv, _ := new(big.Rat).SetString(b.AmountUSD)
			if amountOrder := bv.Cmp(av); amountOrder != 0 {
				return amountOrder
			}
			if requestOrder := cmp.Compare(b.Requests, a.Requests); requestOrder != 0 {
				return requestOrder
			}
			return cmp.Compare(a.TokenID, b.TokenID)
		})
	}
	sort.Slice(result.ByModel, func(i, j int) bool {
		a, _ := new(big.Rat).SetString(result.ByModel[i].AmountUSD)
		b, _ := new(big.Rat).SetString(result.ByModel[j].AmountUSD)
		if a.Cmp(b) != 0 {
			return a.Cmp(b) > 0
		}
		return result.ByModel[i].Provider+result.ByModel[i].Model < result.ByModel[j].Provider+result.ByModel[j].Model
	})
	return result, nil
}

func (s *Store) ObserveSubscriptionUsage(ctx context.Context, service contract.Service, usage contract.SubscriptionUsage) error {
	if service.ID != usage.ServiceID {
		return fmt.Errorf("usage service mismatch")
	}
	if err := usage.Validate(); err != nil {
		return err
	}
	usageJSON, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO model_routing_quota(service_id,document_json) VALUES(?,?) ON CONFLICT(service_id) DO UPDATE SET document_json=excluded.document_json WHERE julianday(json_extract(excluded.document_json,'$.fetched_at')) >= COALESCE(julianday(json_extract(model_routing_quota.document_json,'$.fetched_at')), 0)`, service.ID, string(usageJSON)); err != nil {
		return err
	}
	key := pricing.AccountKey(service)
	windows := map[string]*contract.RateLimitWindow{"primary": usage.Primary, "secondary": usage.Secondary}
	for _, extra := range usage.AdditionalRateLimits {
		windows[extra.LimitName+"/primary"] = extra.Primary
		windows[extra.LimitName+"/secondary"] = extra.Secondary
	}
	for kind, w := range windows {
		if w == nil || w.LimitWindowSeconds == nil {
			continue
		}
		end := w.ResetAt
		if end == nil && w.ResetAfterSeconds != nil {
			v := usage.FetchedAt.Add(time.Duration(*w.ResetAfterSeconds) * time.Second)
			end = &v
		}
		if end == nil || !end.After(usage.FetchedAt) {
			continue
		}
		start := end.Add(-time.Duration(*w.LimitWindowSeconds) * time.Second)
		var reset string
		err := s.db.QueryRowContext(ctx, `SELECT MAX(reset_at) FROM billing_resets WHERE service_id=? AND account_key=? AND reset_at>? AND reset_at<=?`, service.ID, key, billingTime(start), billingTime(usage.FetchedAt)).Scan(&reset)
		if err == nil && reset != "" {
			start, _ = time.Parse(time.RFC3339Nano, reset)
		}
		id := pricing.Digest(string(service.ID), key, kind, billingTime(start), billingTime(*end))
		_, err = s.db.ExecContext(ctx, `INSERT INTO billing_periods(id,service_id,account_key,kind,start_at,end_at,first_observed_at,observed_at,used_percent) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET observed_at=excluded.observed_at,used_percent=excluded.used_percent WHERE closed=0 AND excluded.observed_at>billing_periods.observed_at`, id, service.ID, key, kind, billingTime(start), billingTime(*end), billingTime(usage.FetchedAt), billingTime(usage.FetchedAt), w.UsedPercent)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) ObserveSubscriptionReset(ctx context.Context, service contract.Service) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := billingTime(s.now())
	key := pricing.AccountKey(service)
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO billing_resets VALUES(?,?,?)`, service.ID, key, at); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE billing_periods SET end_at=?,closed=1 WHERE service_id=? AND account_key=? AND start_at<? AND end_at>? AND closed=0`, at, service.ID, key, at, at); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM model_routing_quota WHERE service_id=?`, service.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ServiceBilling(ctx context.Context, id contract.ServiceID) (pricing.ServiceReport, error) {
	result := pricing.ServiceReport{Periods: []pricing.Period{}}
	service, err := s.GetService(ctx, id)
	if err != nil {
		return result, err
	}
	result.Config, err = s.PricingConfig(ctx, id)
	if err != nil {
		return result, err
	}
	key := pricing.AccountKey(service.Service)
	now := s.now().UTC()
	var since string
	if err = s.db.QueryRowContext(ctx, `SELECT value FROM billing_metadata WHERE key='recording_since'`).Scan(&since); err != nil {
		return result, err
	}
	recordingSince, _ := time.Parse(time.RFC3339Nano, since)
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,start_at,end_at,observed_at,used_percent FROM billing_periods WHERE service_id=? AND account_key=? ORDER BY end_at DESC LIMIT 60`, id, key)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var p pricing.Period
		var start, end, observed string
		var used float64
		if err = rows.Scan(&p.ID, &p.Kind, &start, &end, &observed, &used); err != nil {
			rows.Close()
			return result, err
		}
		p.Start, _ = time.Parse(time.RFC3339Nano, start)
		p.End, _ = time.Parse(time.RFC3339Nano, end)
		o, _ := time.Parse(time.RFC3339Nano, observed)
		p.ObservedAt = &o
		p.UsedPercent = &used
		p.Coverage = "observed"
		result.Periods = append(result.Periods, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	// Monthly periods are calendar billing cycles, separate from official limits.
	anchor := now
	for i := 0; i < 12; i++ {
		start, end := pricing.MonthBounds(anchor, result.Config.BillingDay, result.Config.TimeZone)
		p := pricing.Period{ID: "month/" + billingTime(start), Kind: "month", Start: start, End: end, Coverage: "recorded"}
		if i == 0 {
			p.BudgetUSD = result.Config.MonthlyBudgetUSD
		}
		result.Periods = append(result.Periods, p)
		anchor = start.Add(-time.Second)
	}
	periods := result.Periods[:0]
	for _, p := range result.Periods {
		p.Summary, err = s.BillingSummary(ctx, id, key, p.Start, p.End, pricing.BillingSummaryOptions{})
		if err != nil {
			return result, err
		}
		if p.Start.Before(recordingSince) {
			p.Coverage = "partial"
		}
		if p.Kind == "month" && p.End.Before(now) && p.Summary.Requests == 0 {
			continue
		}
		if p.BudgetUSD != "" {
			budget, _ := pricing.ParseUSD(p.BudgetUSD)
			amount, _ := new(big.Rat).SetString(p.Summary.AmountUSD)
			p.RemainingUSD = new(big.Rat).Sub(budget, amount).FloatString(9)
		}
		periods = append(periods, p)
	}
	result.Periods = periods
	return result, nil
}
