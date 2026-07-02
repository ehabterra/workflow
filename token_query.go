package workflow

// Token queries, aggregation, and transformation.
//
// These operate on colored (data-carrying) tokens; uncolored presence tokens are
// skipped, since they carry nothing to query or aggregate.

// FindTokens returns, grouped by place, the colored tokens across the whole
// marking that match pred (a nil pred matches every colored token). Places with
// no match are omitted.
func (w *Workflow) FindTokens(pred TokenPredicate) map[Place][]Token {
	w.mu.RLock()
	defer w.mu.RUnlock()

	out := make(map[Place][]Token)
	for place, toks := range w.marking.AllTokens() {
		var matched []Token
		for _, t := range toks {
			if !isColored(t) {
				continue
			}
			if pred == nil || pred(t) {
				matched = append(matched, t)
			}
		}
		if len(matched) > 0 {
			out[place] = matched
		}
	}
	return out
}

// CountTokens returns the total number of colored tokens across all places that
// match pred (a nil pred counts every colored token).
func (w *Workflow) CountTokens(pred TokenPredicate) int {
	count := 0
	for _, toks := range w.FindTokens(pred) {
		count += len(toks)
	}
	return count
}

// TokenAggregate is the result of AggregateTokens over a numeric token field.
// Min, Max, and Avg are meaningful only when Count > 0.
type TokenAggregate struct {
	Count int
	Sum   float64
	Min   float64
	Max   float64
	Avg   float64
}

// AggregateTokens computes count/sum/min/max/avg over the given numeric field of
// the colored tokens matching pred. Tokens lacking the field, or whose value is
// not numeric, are ignored. With no matching numeric values the zero aggregate is
// returned.
func (w *Workflow) AggregateTokens(pred TokenPredicate, field string) TokenAggregate {
	var agg TokenAggregate
	first := true
	for _, toks := range w.FindTokens(pred) {
		for _, t := range toks {
			v, ok := t.Get(field)
			if !ok {
				continue
			}
			f, ok := toFloat(v)
			if !ok {
				continue
			}
			agg.Count++
			agg.Sum += f
			if first || f < agg.Min {
				agg.Min = f
			}
			if first || f > agg.Max {
				agg.Max = f
			}
			first = false
		}
	}
	if agg.Count > 0 {
		agg.Avg = agg.Sum / float64(agg.Count)
	}
	return agg
}

// TransformTokens replaces the data of each colored token at place that matches
// pred (a nil pred matches all) with transform(token), keeping each token's
// identity. It returns the number of tokens transformed.
func (w *Workflow) TransformTokens(place Place, pred TokenPredicate, transform func(Token) TokenData) int {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := 0
	for _, t := range w.marking.TokensAt(place) {
		if !isColored(t) {
			continue
		}
		if pred != nil && !pred(t) {
			continue
		}
		newData := transform(t)
		_ = w.marking.RemoveToken(place, t.ID())
		w.marking.AddToken(place, t.WithData(newData))
		n++
	}
	return n
}

// toFloat coerces common numeric types (including json.Number-style float64 from
// decoded JSON) to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
