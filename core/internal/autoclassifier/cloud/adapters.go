package cloud

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
)

// jevRequest is the Jev Decisions call shape: one state string and a question
// map keyed by question name, whose criteria map is keyed by exact label.
type jevRequest struct {
	State     string                 `json:"state"`
	Model     string                 `json:"model"`
	Questions map[string]jevQuestion `json:"questions"`
}

type jevQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type jevResponse struct {
	Answers map[string]jevAnswer `json:"answers"`
}

type jevAnswer struct {
	Choice     *string        `json:"choice"`
	Confidence *float64       `json:"confidence"`
	Ranked     []rankedChoice `json:"ranked,omitempty"`
}

func buildJevRequest(in Input) (string, []byte, error) {
	request := jevRequest{
		State:     in.Text,
		Model:     in.Model,
		Questions: jevQuestions(in),
	}
	body, err := marshalRequestBody(request)
	if err != nil {
		return "", nil, err
	}
	return in.Endpoint, body, nil
}

// jevQuestions builds the request's question map: the built-in task_type plus
// one question per custom dimension. Jev keys questions by name, so a
// dimension's name is its question key and answers come back under the same
// key — no ordering contract is needed on this provider.
//
// With no custom dimensions this returns exactly the one-entry map the
// single-dimension request always sent.
func jevQuestions(in Input) map[string]jevQuestion {
	questions := make(map[string]jevQuestion, len(in.Dimensions)+1)
	questions["task_type"] = jevQuestion{
		Type:         "choice",
		Instructions: in.Prompt,
		Criteria:     labelDescriptions(in.Labels),
	}
	for _, dimension := range in.Dimensions {
		if dimension.Name == "task_type" {
			continue
		}
		criteria := make(map[string]string, len(dimension.Options))
		for _, option := range dimension.Options {
			criteria[option.Name] = option.Description
		}
		// An empty description still sends the option: the model must be able
		// to choose it, and an empty description is a valid thing to send.
		questions[dimension.Name] = jevQuestion{
			Type:         "choice",
			Instructions: dimensionQuestionInstructions(dimension),
			Criteria:     criteria,
		}
	}
	return questions
}

// dimensionQuestionInstructions is the instruction one custom dimension's
// question carries. It is deliberately one sentence built from the user's own
// dimension name and description, with no gateway identity and no splice of
// the user text: the user text travels as the request's state, exactly as it
// does for task_type.
func dimensionQuestionInstructions(dimension contract.ClassifierDimension) string {
	if dimension.Description == "" {
		return fmt.Sprintf("Choose the %s that best applies to the state.", dimension.Name)
	}
	return fmt.Sprintf("Choose the %s that best applies to the state. %s", dimension.Name, dimension.Description)
}

func parseJevResponse(payload []byte, labels []string) (string, float64, []autoclassifier.Candidate, error) {
	var response jevResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return "", 0, nil, errInvalidOutput
	}
	answer, ok := response.Answers["task_type"]
	if !ok {
		return "", 0, nil, errInvalidOutput
	}
	return validatedChoiceWithRanked(answer.Choice, answer.Confidence, answer.Ranked, labels)
}

// parseJevDimensions reads the custom dimensions out of a reply that was asked
// for them. task_type is deliberately absent from the result: it is validated
// by parseJevResponse through the shared answer contract, and its value must
// not appear twice.
//
// A dimension the provider did not answer is simply missing from the returned
// map, which the caller turns into an unknown dimension. A dimension answered
// with a choice outside its own option list is invalid output for the whole
// reply — the answer is one document, and a partially readable answer is not a
// safe basis for routing.
func parseJevDimensions(payload []byte, in Input) (map[string]autoclassifier.DimensionAnswer, error) {
	var response jevResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, errInvalidOutput
	}
	answers := make(map[string]autoclassifier.DimensionAnswer, len(in.Dimensions))
	for _, dimension := range in.Dimensions {
		if dimension.Name == "task_type" {
			continue
		}
		answer, ok := response.Answers[dimension.Name]
		if !ok {
			continue
		}
		if answer.Choice == nil || *answer.Choice == "" {
			continue
		}
		option := dimensionOptionNames(dimension)
		label, score, err := validatedChoice(answer.Choice, answer.Confidence, option)
		if err != nil {
			return nil, err
		}
		answers[dimension.Name] = autoclassifier.DimensionAnswer{Label: label, Score: score}
	}
	return answers, nil
}

func dimensionOptionNames(dimension contract.ClassifierDimension) []string {
	options := make([]string, 0, len(dimension.Options))
	for _, option := range dimension.Options {
		options = append(options, option.Name)
	}
	return options
}

// openAIDecisionsRequest is the OpenAI Decisions call shape: questions are an
// array in label order and answers align with that order.
type openAIDecisionsRequest struct {
	Model     string                    `json:"model"`
	Input     string                    `json:"input"`
	Questions []openAIDecisionsQuestion `json:"questions"`
}

type openAIDecisionsQuestion struct {
	Type         string                  `json:"type"`
	Name         string                  `json:"name"`
	Instructions string                  `json:"instructions"`
	Choices      []openAIDecisionsChoice `json:"choices"`
}

type openAIDecisionsChoice struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

type openAIDecisionsResponse struct {
	Answers []openAIDecisionsAnswer `json:"answers"`
}

type openAIDecisionsAnswer struct {
	Type       string         `json:"type"`
	Name       *string        `json:"name"`
	Choice     *string        `json:"choice"`
	Confidence *float64       `json:"confidence"`
	Ranked     []rankedChoice `json:"ranked,omitempty"`
}

func buildOpenAIDecisionsRequest(in Input) (string, []byte, error) {
	request := openAIDecisionsRequest{
		Model:     in.Model,
		Input:     in.Text,
		Questions: openAIDecisionsQuestions(in),
	}
	body, err := marshalRequestBody(request)
	if err != nil {
		return "", nil, err
	}
	return in.Endpoint, body, nil
}

// openAIDecisionsQuestions builds the questions array: task_type first, then
// one question per custom dimension, in declaration order. Order is part of the
// contract on this provider — the guide promises answers come back in the order
// the questions were asked — and it is also the index-alignment fallback for a
// reply that omits names. Keeping task_type at index 0 means the fallback
// always resolves task_type to the same slot.
//
// With no custom dimensions this is the one-question array the
// single-dimension request always sent.
func openAIDecisionsQuestions(in Input) []openAIDecisionsQuestion {
	questions := make([]openAIDecisionsQuestion, 0, len(in.Dimensions)+1)
	questions = append(questions, openAIDecisionsQuestion{
		Type:         "choice",
		Name:         "task_type",
		Instructions: in.Prompt,
		Choices:      labelChoices(in.Labels),
	})
	for _, dimension := range in.Dimensions {
		if dimension.Name == "task_type" {
			continue
		}
		// Each option carries its own description, which is the only place the
		// model learns what the option means; the name is the value it returns.
		choices := make([]openAIDecisionsChoice, 0, len(dimension.Options))
		for _, option := range dimension.Options {
			choices = append(choices, openAIDecisionsChoice{Value: option.Name, Description: option.Description})
		}
		questions = append(questions, openAIDecisionsQuestion{
			Type:         "choice",
			Name:         dimension.Name,
			Instructions: dimensionQuestionInstructions(dimension),
			Choices:      choices,
		})
	}
	return questions
}

// labelChoices renders the task_type label set as choices whose description is
// the label itself — labels carry no per-label description in V1.
func labelChoices(labels []string) []openAIDecisionsChoice {
	choices := make([]openAIDecisionsChoice, 0, len(labels))
	for _, label := range labels {
		choices = append(choices, openAIDecisionsChoice{Value: label, Description: label})
	}
	return choices
}

func parseOpenAIDecisionsResponse(payload []byte, labels []string) (string, float64, []autoclassifier.Candidate, error) {
	var response openAIDecisionsResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return "", 0, nil, errInvalidOutput
	}
	answer, ok := findOpenAIDecisionsAnswer(response.Answers, "task_type", 0)
	if !ok {
		return "", 0, nil, errInvalidOutput
	}
	return answerToChoice(answer, labels)
}

// findOpenAIDecisionsAnswer locates one question's answer. Name matching comes
// first and is strict: with several questions in flight, an answer that names a
// different question must never be read as this one, and an answer that names
// nothing at all is not evidence of anything either. Only when the reply names
// no answers whatsoever does the documented ordering guarantee apply and the
// position resolves the question — which is why the request always asks
// task_type first.
func findOpenAIDecisionsAnswer(answers []openAIDecisionsAnswer, name string, position int) (openAIDecisionsAnswer, bool) {
	for _, answer := range answers {
		if answer.Name != nil && *answer.Name == name {
			return answer, true
		}
	}
	named := false
	for _, answer := range answers {
		if answer.Name != nil && *answer.Name != "" {
			named = true
			break
		}
	}
	if named {
		// The provider named its answers but not this one, so this question went
		// unanswered. Position is not a safe substitute here: the indexes of the
		// answers that were named are unknown.
		return openAIDecisionsAnswer{}, false
	}
	if position < 0 || position >= len(answers) {
		return openAIDecisionsAnswer{}, false
	}
	return answers[position], true
}

// parseOpenAIDecisionsDimensions reads the custom dimensions out of a reply
// that was asked for them. task_type is deliberately absent from the result: it
// is validated by parseOpenAIDecisionsResponse through the shared answer
// contract, and its value must not appear twice.
//
// An unanswered dimension is missing from the map, which the caller turns into
// an unknown dimension. A dimension answered with a value outside its own option
// list is invalid output for the whole reply — the answer is one document, and a
// partially readable answer is not a safe basis for routing.
func parseOpenAIDecisionsDimensions(payload []byte, in Input) (map[string]autoclassifier.DimensionAnswer, error) {
	var response openAIDecisionsResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, errInvalidOutput
	}
	answers := make(map[string]autoclassifier.DimensionAnswer, len(in.Dimensions))
	position := 1 // task_type occupies index 0 and was asked first
	for _, dimension := range in.Dimensions {
		if dimension.Name == "task_type" {
			continue
		}
		answer, ok := findOpenAIDecisionsAnswer(response.Answers, dimension.Name, position)
		position++
		if !ok {
			continue
		}
		if answer.Type == "refusal" {
			// The guide is explicit that one question's refusal leaves the
			// others answered, so this is not a failed reply: the dimension
			// simply has no value, exactly like an unanswered one.
			continue
		}
		if answer.Choice == nil || *answer.Choice == "" {
			continue
		}
		option := dimensionOptionNames(dimension)
		label, score, err := validatedChoice(answer.Choice, answer.Confidence, option)
		if err != nil {
			return nil, err
		}
		answers[dimension.Name] = autoclassifier.DimensionAnswer{Label: label, Score: score}
	}
	return answers, nil
}

func answerToChoice(answer openAIDecisionsAnswer, labels []string) (string, float64, []autoclassifier.Candidate, error) {
	if answer.Type == "refusal" {
		return "", 0, nil, errRefusal
	}
	return validatedChoiceWithRanked(answer.Choice, answer.Confidence, answer.Ranked, labels)
}

// rankedChoice is one entry of a provider's optional ranked answer list,
// carrying the same field names as the single choice above.
type rankedChoice struct {
	Choice     *string  `json:"choice"`
	Confidence *float64 `json:"confidence"`
}

// validatedChoiceWithRanked enforces the shared answer contract — a choice
// inside the effective label set and a finite confidence inside [0,1] — and
// additionally folds the provider's optional ranked list into the Outcome's
// candidate sequence. The primary choice stays the category. An absent, empty,
// or fully invalid ranked list leaves the candidates empty so callers fall
// back to the single-value form. A ranked list that survives normalization
// while omitting the primary is completed, not trusted: the primary is merged
// back in with its own score, so the presence of a ranked field can never
// displace a valid primary from the candidate sequence. When the ranked list
// already describes the primary, the ranked score is authoritative and the
// primary's own score does not overwrite it.
func validatedChoiceWithRanked(choice *string, confidence *float64, ranked []rankedChoice, labels []string) (string, float64, []autoclassifier.Candidate, error) {
	category, score, err := validatedChoice(choice, confidence, labels)
	if err != nil {
		return "", 0, nil, err
	}
	return category, score, candidatesWithPrimary(ranked, category, score, labels), nil
}

// candidatesWithPrimary layers the ranked list strictly on top of the
// single-value answer, never in place of it. A ranked list with no survivors
// (absent, empty, or fully invalid) still yields no candidates, preserving the
// single-value fallback. A surviving ranked list that omits the primary gets
// the primary merged in with its own score and the sequence re-sorted by the
// same rules normalizeRanked applies, so a valid-but-unranked primary still
// reaches selection. A ranked list that already describes the primary stands
// as-is — its score is kept, never overwritten by the primary's confidence.
// The low-confidence threshold stays out of this layer: a merged primary below
// threshold is filtered downstream in modelrouting.TaskTypeCandidateScores
// like any other entry.
func candidatesWithPrimary(entries []rankedChoice, category string, score float64, labels []string) []autoclassifier.Candidate {
	candidates := normalizeRanked(entries, labels)
	if len(candidates) == 0 {
		return nil
	}
	for _, candidate := range candidates {
		if candidate.Label == category {
			return candidates
		}
	}
	// The primary is a label absent from the sequence, so uniqueness and the
	// keeps-best-entry rule both hold after the merge; only the ordering has
	// to be redone.
	return sortRankedCandidates(append(candidates, autoclassifier.Candidate{Label: category, Score: score}), labels)
}

// normalizeRanked turns a provider's optional ranked list into the Outcome's
// candidate sequence, honoring the Outcome contract that the producing source
// sorts, deduplicates, and catalog-checks: entries outside the requested
// label set or with invalid scores drop, survivors sort by score descending
// with request order breaking ties, and each label keeps only its best entry.
// The low-confidence threshold is deliberately NOT applied here — threshold
// filtering belongs to the one conversion shared by the inference path and
// the control-plane dry-run (modelrouting.TaskTypeCandidateScores).
func normalizeRanked(entries []rankedChoice, labels []string) []autoclassifier.Candidate {
	if len(entries) == 0 {
		return nil
	}
	requestOrder := make(map[string]int, len(labels))
	for index, label := range labels {
		requestOrder[label] = index
	}
	best := make(map[string]float64, len(labels))
	for _, entry := range entries {
		if entry.Choice == nil || *entry.Choice == "" || entry.Confidence == nil {
			continue
		}
		if _, ok := requestOrder[*entry.Choice]; !ok {
			continue
		}
		if math.IsNaN(*entry.Confidence) || math.IsInf(*entry.Confidence, 0) || *entry.Confidence < 0 || *entry.Confidence > 1 {
			continue
		}
		if existing, ok := best[*entry.Choice]; !ok || *entry.Confidence > existing {
			best[*entry.Choice] = *entry.Confidence
		}
	}
	if len(best) == 0 {
		return nil
	}
	candidates := make([]autoclassifier.Candidate, 0, len(best))
	for label, score := range best {
		candidates = append(candidates, autoclassifier.Candidate{Label: label, Score: score})
	}
	return sortRankedCandidates(candidates, labels)
}

// sortRankedCandidates applies the Outcome's ordering contract: score
// descending, request order breaking ties. Both normalizeRanked's survivors
// and a primary merged into an already-normalized sequence go through this
// one comparator so the ordering rules cannot drift apart.
func sortRankedCandidates(candidates []autoclassifier.Candidate, labels []string) []autoclassifier.Candidate {
	requestOrder := make(map[string]int, len(labels))
	for index, label := range labels {
		requestOrder[label] = index
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return requestOrder[candidates[i].Label] < requestOrder[candidates[j].Label]
	})
	return candidates
}

// validatedChoice enforces the shared answer contract: a choice inside the
// effective label set and a finite confidence inside [0,1]. Anything else is
// invalid output, which the caller records as a deterministic fallback.
func validatedChoice(choice *string, confidence *float64, labels []string) (string, float64, error) {
	if choice == nil || *choice == "" {
		return "", 0, errInvalidOutput
	}
	if confidence == nil || math.IsNaN(*confidence) || math.IsInf(*confidence, 0) || *confidence < 0 || *confidence > 1 {
		return "", 0, errInvalidOutput
	}
	for _, label := range labels {
		if label == *choice {
			return *choice, *confidence, nil
		}
	}
	return "", 0, errInvalidOutput
}

var errInvalidOutput = fmt.Errorf("classifier answer is not a valid choice")
