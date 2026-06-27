package pricing

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
)

const microsPerUSD = int64(1_000_000)

// ModelPrice is the per-1M-token price in USD.
type ModelPrice struct {
	InputPerM       string `json:"input"`
	OutputPerM      string `json:"output"`
	CachedInputPerM string `json:"cached_input,omitempty"`
}

func (p *ModelPrice) UnmarshalJSON(data []byte) error {
	var raw struct {
		Input      json.RawMessage `json:"input"`
		Output     json.RawMessage `json:"output"`
		Cached     json.RawMessage `json:"cached_input"`
		InputPerM  json.RawMessage `json:"input_per_m"`
		OutputPerM json.RawMessage `json:"output_per_m"`
		CachedPerM json.RawMessage `json:"cached_input_per_m"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	input := raw.Input
	if len(input) == 0 {
		input = raw.InputPerM
	}
	output := raw.Output
	if len(output) == 0 {
		output = raw.OutputPerM
	}
	cached := raw.Cached
	if len(cached) == 0 {
		cached = raw.CachedPerM
	}
	var err error
	p.InputPerM, err = decodePriceValue(input)
	if err != nil {
		return fmt.Errorf("input: %w", err)
	}
	p.OutputPerM, err = decodePriceValue(output)
	if err != nil {
		return fmt.Errorf("output: %w", err)
	}
	p.CachedInputPerM, err = decodePriceValue(cached)
	if err != nil {
		return fmt.Errorf("cached_input: %w", err)
	}
	return nil
}

type compiledPrice struct {
	InputMicrosPerM       int64
	OutputMicrosPerM      int64
	CachedInputMicrosPerM int64
}

type ModelPriceEntry struct {
	Model       string `json:"model"`
	Input       string `json:"input"`
	Output      string `json:"output"`
	CachedInput string `json:"cached_input,omitempty"`
	IsDefault   bool   `json:"isDefault,omitempty"`
}

// Usage is the token accounting extracted from an LLM response.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
}

// Table resolves model ids to per-token prices. Exact matches win; otherwise
// the longest configured model-prefix match is used.
type Table struct {
	defaultPrice compiledPrice
	models       map[string]compiledPrice
	prefixes     []string
}

// DefaultTable returns the built-in fallback table used to initialize storage.
func DefaultTable() (*Table, error) {
	return NewTable(DefaultPrice(), DefaultPrices())
}

func DefaultPrice() ModelPrice {
	return ModelPrice{InputPerM: "5.00", OutputPerM: "15.00"}
}

func DefaultPrices() map[string]ModelPrice {
	return map[string]ModelPrice{
		"gpt-4o":            {InputPerM: "2.50", OutputPerM: "10.00"},
		"gpt-4o-mini":       {InputPerM: "0.15", OutputPerM: "0.60"},
		"gpt-4.1":           {InputPerM: "2.00", OutputPerM: "8.00"},
		"gpt-4.1-mini":      {InputPerM: "0.40", OutputPerM: "1.60"},
		"gpt-4.1-nano":      {InputPerM: "0.10", OutputPerM: "0.40"},
		"gpt-5":             {InputPerM: "1.25", OutputPerM: "10.00"},
		"gpt-5-mini":        {InputPerM: "0.25", OutputPerM: "2.00"},
		"gpt-5-nano":        {InputPerM: "0.05", OutputPerM: "0.40"},
		"gpt-5.5":           {InputPerM: "5.00", CachedInputPerM: "0.50", OutputPerM: "30.00"},
		"gpt-5.5-pro":       {InputPerM: "30.00", OutputPerM: "180.00"},
		"claude-opus-4":     {InputPerM: "15.00", OutputPerM: "75.00"},
		"claude-sonnet-4":   {InputPerM: "3.00", OutputPerM: "15.00"},
		"claude-haiku-4.5":  {InputPerM: "1.00", OutputPerM: "5.00"},
		"claude-haiku-4-5":  {InputPerM: "1.00", OutputPerM: "5.00"},
		"deepseek-chat":     {InputPerM: "0.27", OutputPerM: "1.10"},
		"deepseek-reasoner": {InputPerM: "0.55", OutputPerM: "2.19"},
	}
}

func NewTable(defaultPrice ModelPrice, models map[string]ModelPrice) (*Table, error) {
	if defaultPrice.InputPerM == "" {
		defaultPrice.InputPerM = "5"
	}
	if defaultPrice.OutputPerM == "" {
		defaultPrice.OutputPerM = "15"
	}
	d, err := compilePrice(defaultPrice)
	if err != nil {
		return nil, fmt.Errorf("default price: %w", err)
	}
	t := &Table{
		defaultPrice: d,
		models:       make(map[string]compiledPrice, len(models)),
	}
	for model, price := range models {
		key := normalizeModel(model)
		if key == "" {
			return nil, fmt.Errorf("empty model id")
		}
		compiled, err := compilePrice(price)
		if err != nil {
			return nil, fmt.Errorf("model %q: %w", model, err)
		}
		t.models[key] = compiled
		t.prefixes = append(t.prefixes, key)
	}
	sort.Slice(t.prefixes, func(i, j int) bool {
		return len(t.prefixes[i]) > len(t.prefixes[j])
	})
	return t, nil
}

func NewTableFromEntries(defaultPrice ModelPrice, entries []ModelPriceEntry) (*Table, error) {
	models := make(map[string]ModelPrice, len(entries))
	for _, entry := range entries {
		models[entry.Model] = ModelPrice{
			InputPerM:       entry.Input,
			OutputPerM:      entry.Output,
			CachedInputPerM: entry.CachedInput,
		}
	}
	return NewTable(defaultPrice, models)
}

func DefaultEntries() []ModelPriceEntry {
	defaults := DefaultPrices()
	out := make([]ModelPriceEntry, 0, len(defaults))
	for model, price := range defaults {
		out = append(out, ModelPriceEntry{
			Model:       model,
			Input:       price.InputPerM,
			Output:      price.OutputPerM,
			CachedInput: price.CachedInputPerM,
			IsDefault:   true,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

func compilePrice(p ModelPrice) (compiledPrice, error) {
	input, err := parseUSDToMicros(p.InputPerM)
	if err != nil {
		return compiledPrice{}, fmt.Errorf("input: %w", err)
	}
	output, err := parseUSDToMicros(p.OutputPerM)
	if err != nil {
		return compiledPrice{}, fmt.Errorf("output: %w", err)
	}
	cachedInput := input
	if strings.TrimSpace(p.CachedInputPerM) != "" {
		cachedInput, err = parseUSDToMicros(p.CachedInputPerM)
		if err != nil {
			return compiledPrice{}, fmt.Errorf("cached_input: %w", err)
		}
	}
	return compiledPrice{InputMicrosPerM: input, OutputMicrosPerM: output, CachedInputMicrosPerM: cachedInput}, nil
}

func parseUSDToMicros(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty price")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("invalid price %q", s)
	}
	if r.Sign() < 0 {
		return 0, fmt.Errorf("price must be >= 0")
	}
	r.Mul(r, big.NewRat(microsPerUSD, 1))
	if !r.IsInt() {
		return 0, fmt.Errorf("price %q has more than 6 decimal places", s)
	}
	return r.Num().Int64(), nil
}

func decodePriceValue(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	return string(raw), nil
}

func (t *Table) priceFor(model string) compiledPrice {
	if t == nil {
		t, _ = DefaultTable()
	}
	key := normalizeModel(model)
	if p, ok := t.models[key]; ok {
		return p
	}
	for _, prefix := range t.prefixes {
		if strings.HasPrefix(key, prefix) {
			return t.models[prefix]
		}
	}
	return t.defaultPrice
}

// CostMicroUSDC returns the price in USDC base units (6 decimals), rounded up.
func (t *Table) CostMicroUSDC(model string, u Usage, markup float64) *big.Int {
	p := t.priceFor(model)
	promptTokens := max(u.PromptTokens, 0)
	cachedTokens := min(max(u.CachedTokens, 0), promptTokens)
	uncachedTokens := promptTokens - cachedTokens
	input := new(big.Int).Mul(big.NewInt(int64(uncachedTokens)), big.NewInt(p.InputMicrosPerM))
	cachedInput := new(big.Int).Mul(big.NewInt(int64(cachedTokens)), big.NewInt(p.CachedInputMicrosPerM))
	output := new(big.Int).Mul(big.NewInt(int64(max(u.CompletionTokens, 0))), big.NewInt(p.OutputMicrosPerM))
	total := new(big.Int).Add(input, cachedInput)
	total.Add(total, output)
	total.Add(total, big.NewInt(999_999))
	total.Div(total, big.NewInt(1_000_000))
	if markup > 0 && markup != 1 {
		markupRat := new(big.Rat).SetFloat64(markup)
		if markupRat == nil {
			markupRat = big.NewRat(1, 1)
		}
		r := new(big.Rat).SetInt(total)
		r.Mul(r, markupRat)
		total = ceilRat(r)
	}
	if total.Sign() == 0 && (u.PromptTokens > 0 || u.CompletionTokens > 0) {
		return big.NewInt(1)
	}
	return total
}

type Manager struct {
	mu    sync.RWMutex
	table *Table
}

func NewManager(table *Table) *Manager {
	if table == nil {
		table, _ = DefaultTable()
	}
	return &Manager{table: table}
}

func (m *Manager) Replace(table *Table) {
	if table == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.table = table
}

func (m *Manager) CostMicroUSDC(model string, u Usage, markup float64) *big.Int {
	m.mu.RLock()
	table := m.table
	m.mu.RUnlock()
	return table.CostMicroUSDC(model, u, markup)
}

// CostMicroUSDC uses the built-in default table.
func CostMicroUSDC(model string, u Usage, markup float64) *big.Int {
	t, _ := DefaultTable()
	return t.CostMicroUSDC(model, u, markup)
}

func ceilRat(r *big.Rat) *big.Int {
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if new(big.Int).Mod(r.Num(), r.Denom()).Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

func normalizeModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// USDCUnitsToUSD formats USDC base units (6 decimals) as a human dollar string.
func USDCUnitsToUSD(units *big.Int) string {
	if units == nil {
		return "0"
	}
	q := new(big.Rat).SetFrac(units, big.NewInt(1_000_000))
	return q.FloatString(6)
}
