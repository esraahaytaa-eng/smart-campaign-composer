// Command interpret-eval runs the labeled briefs in evals/interpret through
// the production interpretation path and scores the answers (D75).
//
// A real run calls Claude and costs money, so it runs only on purpose (make
// eval), never in make test or CI. -mock checks the wiring for free.
// "interpret-eval compare A.json B.json" diffs two saved runs.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"smart-campaign-composer/internal/campaign"
	"smart-campaign-composer/internal/interpreteval"
	"smart-campaign-composer/internal/llm"
)

const defaultModel = "claude-opus-5-5" // as in config (D23)

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "compare" {
		err = compare(os.Args[2:])
	} else {
		err = run(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "interpret-eval:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("interpret-eval", flag.ContinueOnError)
	casesPath := fs.String("cases", "evals/interpret/cases.json", "labeled dataset")
	outDir := fs.String("out", "evals/interpret/results", "directory for the saved run (real runs only)")
	model := fs.String("model", envOr("ANTHROPIC_MODEL", defaultModel), "Claude model")
	maxUSD := fs.Float64("max-usd", 10, "hard budget: a case starts only if its worst case still fits")
	preflight := fs.Bool("preflight", false, "only verify the key and model and count input tokens (free), then stop")
	parallel := fs.Int("parallel", 4, "cases run at the same time")
	timeout := fs.Duration("timeout", 60*time.Second, "per AI call, as LLM_TIMEOUT")
	commit := fs.String("commit", "", "git commit being evaluated (make eval sets it)")
	mock := fs.Bool("mock", false, "use the mock AI: free, checks the wiring only, nothing saved")
	var p prices
	fs.Float64Var(&p.input, "input-usd", 4, "USD per million input tokens (default: claude-opus-5-5)")
	fs.Float64Var(&p.output, "output-usd", 20, "USD per million output tokens (default: claude-opus-5-5)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *parallel < 1 || *maxUSD <= 0 {
		return errors.New("-parallel must be at least 1 and -max-usd above 0")
	}
	explicitPrices := false
	fs.Visit(func(f *flag.Flag) { explicitPrices = explicitPrices || f.Name == "input-usd" || f.Name == "output-usd" })

	data, err := os.ReadFile(*casesPath)
	if err != nil {
		return err
	}
	ds, err := interpreteval.Parse(data)
	if err != nil {
		return fmt.Errorf("dataset %s:\n%w", *casesPath, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m := &meter{}
	b := &budget{max: *maxUSD, perCase: 0, spent: func() float64 { return m.usd(p) }}
	var ai llm.LLM
	mode := "real"
	if *mock {
		ai, mode, *model = llm.NewMock(), "mock", "mock"
	} else {
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return errors.New("ANTHROPIC_API_KEY is not set (make eval reads it from .env)")
		}
		if p, err = pricesFor(*model, explicitPrices, p); err != nil {
			return err
		}
		// Free: proves the key and model work and bounds what each case can cost.
		slog.SetDefault(slog.New(slog.DiscardHandler))
		counter := anthropic.NewClient(option.WithAPIKey(key), option.WithMaxRetries(0))
		ms, err := measure(ctx, ds, *model, &counter)
		if err != nil {
			return fmt.Errorf("preflight: %w", err)
		}
		b.perCase = ms.worstCasePerCase(p)
		fmt.Printf("Preflight: key and %s accepted by count_tokens; input per call %d tokens on average, at most %d; max_tokens %d.\n",
			*model, ms.meanInput, ms.maxInput, ms.maxOutput)
		fmt.Printf("Worst case per case (2 calls at full max_tokens): $%.3f; the $%.2f budget always covers at least %d cases.\n",
			b.perCase, *maxUSD, int(*maxUSD/b.perCase))
		if *preflight {
			return nil
		}
		ai = llm.NewAnthropic(key, *model)
	}

	slog.SetDefault(slog.New(m)) // the per-call log lines carry the token counts
	fmt.Printf("Evaluating %d cases with %s (prompt %s), budget $%.2f...\n", len(ds.Cases), *model, llm.InterpretPromptVersion(), *maxUSD)
	start := time.Now()
	results, skipped := evaluate(ctx, ds, ai, *parallel, *timeout, b)
	sum := sha256.Sum256(data)
	r := interpreteval.Run{
		Mode: mode, Model: *model, Effort: llm.InterpretEffort(), PromptVersion: llm.InterpretPromptVersion(),
		DatasetSHA256: hex.EncodeToString(sum[:]), Commit: *commit, StartedAt: start.UTC().Truncate(time.Second),
		Seconds: time.Since(start).Round(time.Second).Seconds(), InputTokens: m.in.Load(), OutputTokens: m.out.Load(),
		CostUSD: m.usd(p), MaxUSD: *maxUSD, WorstCasePerCaseUSD: b.perCase, UnmeteredMaxUSD: b.unmetered, Skipped: skipped,
		Summary: interpreteval.Summarize(results), Results: results,
	}
	if *mock {
		r.CostUSD = 0
	}

	fmt.Println()
	fmt.Print(interpreteval.Report(results))
	fmt.Printf("\nTokens: %d input, %d output; cost $%.2f; %.0fs\n", r.InputTokens, r.OutputTokens, r.CostUSD, r.Seconds)
	if r.UnmeteredMaxUSD > 0 {
		fmt.Printf("Plus at most $%.2f for failed or timed-out calls whose tokens were not reported.\n", r.UnmeteredMaxUSD)
	}
	if len(skipped) > 0 {
		fmt.Printf("Skipped %d cases (budget or interrupted): %v\n", len(skipped), skipped)
	}
	if *mock {
		fmt.Println("Mock run: the mock knows only the README briefs, so these numbers say nothing about Claude. Nothing saved.")
		return nil
	}
	path, err := save(*outDir, r)
	if err != nil {
		return err
	}
	fmt.Println("Saved", path)
	return nil
}

// evaluate runs the cases through campaign.Interpret, the production path
// with its validation and single retry (D15), at most parallel at a time.
// A case starts only when the budget admits its worst case; if it doesn't,
// the cases in flight finish first and free their reservations, and if it
// still doesn't, the run stops. Cases not run, and any cut off by an
// interrupt, are returned as skipped. Results keep the dataset's order.
func evaluate(ctx context.Context, ds interpreteval.Dataset, ai llm.LLM, parallel int, timeout time.Duration, b *budget) ([]interpreteval.Result, []string) {
	done := make([]*interpreteval.Result, len(ds.Cases))
	slots := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, c := range ds.Cases {
		slots <- struct{}{}
		if ctx.Err() != nil {
			break
		}
		if !b.reserve() {
			wg.Wait()
			if !b.reserve() {
				break
			}
		}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			counted := &counting{LLM: ai}
			in := llm.InterpretInput{Text: c.Brief, Now: ds.Now, TimeZone: ds.TimeZone, Catalog: ds.Catalog}
			got, err := campaign.Interpret(ctx, counted, in, timeout)
			if ctx.Err() != nil {
				b.release(false) // interrupted mid-call: tokens may be billed unreported
				return
			}
			r := interpreteval.Score(c, got, err)
			b.release(r.Outcome != interpreteval.APIError)
			r.Calls = counted.calls
			done[i] = &r
		}()
	}
	wg.Wait()
	results := []interpreteval.Result{}
	var skipped []string
	for i, r := range done {
		if r == nil {
			skipped = append(skipped, ds.Cases[i].ID)
			continue
		}
		results = append(results, *r)
	}
	return results, skipped
}

// budget admits a case only while the most the run can cost stays within
// max: the metered spend, the worst case of every case in flight, the
// unmetered calls, and the new case's worst case. Overcounting is fine;
// spending past max is not.
type budget struct {
	max, perCase float64
	spent        func() float64 // metered so far, from the call logs
	mu           sync.Mutex
	reserved     float64
	unmetered    float64 // worst cases of calls that failed without reporting tokens
}

func (b *budget) reserve() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spent()+b.reserved+b.unmetered+b.perCase > b.max {
		return false
	}
	b.reserved += b.perCase
	return true
}

// release ends a case's reservation. metered is false when a call may have
// been billed without its tokens being logged (an API error, timeout or
// interrupt); then its worst case stays counted.
func (b *budget) release(metered bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved -= b.perCase
	if !metered {
		b.unmetered += b.perCase
	}
}

// measurement bounds one call: its input tokens and its max_tokens.
type measurement struct{ meanInput, maxInput, maxOutput int64 }

// retryMarginTokens covers the validation errors a retry adds to the input (D15).
const retryMarginTokens = 1000

// worstCasePerCase is two calls (the answer and its one retry), each with the
// largest input plus the retry margin and the full max_tokens of output.
func (m measurement) worstCasePerCase(p prices) float64 {
	perCall := (float64(m.maxInput+retryMarginTokens)*p.input + float64(m.maxOutput)*p.output) / 1e6
	return 2 * perCall
}

// measure renders each case's exact request with the production client,
// sent to a local stand-in so nothing is billed, and counts its input tokens
// with the free count_tokens endpoint, which also proves the key and model.
func measure(ctx context.Context, ds interpreteval.Dataset, model string, counter *anthropic.Client) (measurement, error) {
	var body []byte
	stand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"local","type":"message","role":"assistant","model":"local","content":[{"type":"text","text":"{\"unsupported\":true,\"reason\":\"r\",\"rules\":[],\"suggested_send_at\":null,\"schedule_note\":null}"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`))
	}))
	defer stand.Close()
	local := llm.NewAnthropic("local", model, option.WithBaseURL(stand.URL))

	var m measurement
	var total int64
	for _, c := range ds.Cases {
		in := llm.InterpretInput{Text: c.Brief, Now: ds.Now, TimeZone: ds.TimeZone, Catalog: ds.Catalog}
		if _, err := local.InterpretRequest(ctx, in); err != nil {
			return m, fmt.Errorf("render %s: %w", c.ID, err)
		}
		var req map[string]json.RawMessage
		if err := json.Unmarshal(body, &req); err != nil {
			return m, fmt.Errorf("render %s: %w", c.ID, err)
		}
		var maxTokens int64
		if err := json.Unmarshal(req["max_tokens"], &maxTokens); err != nil || maxTokens <= 0 {
			return m, fmt.Errorf("render %s: no max_tokens in the request", c.ID)
		}
		delete(req, "max_tokens") // count_tokens does not take it
		var res struct {
			InputTokens int64 `json:"input_tokens"`
		}
		if err := counter.Post(ctx, "v1/messages/count_tokens", req, &res); err != nil {
			return m, fmt.Errorf("count tokens for %s: %w", c.ID, err)
		}
		total += res.InputTokens
		m.maxInput = max(m.maxInput, res.InputTokens)
		m.maxOutput = max(m.maxOutput, maxTokens)
	}
	if len(ds.Cases) > 0 {
		m.meanInput = total / int64(len(ds.Cases))
	}
	return m, nil
}

// counting counts one case's AI calls; Interpret makes them one at a time.
type counting struct {
	llm.LLM
	calls int
}

func (c *counting) InterpretRequest(ctx context.Context, in llm.InterpretInput) (json.RawMessage, error) {
	c.calls++
	return c.LLM.InterpretRequest(ctx, in)
}

type prices struct{ input, output float64 } // USD per million tokens

// pricesFor refuses a model other than the default without explicit prices:
// the cost cap would otherwise be computed with the wrong prices.
func pricesFor(model string, explicit bool, p prices) (prices, error) {
	if model != defaultModel && !explicit {
		return prices{}, fmt.Errorf("pass -input-usd and -output-usd for %s; the defaults are %s's prices", model, defaultModel)
	}
	return p, nil
}

// meter is a slog handler that adds up the token counts of the real
// client's "claude call" lines (D72) and drops all other logging.
type meter struct{ in, out atomic.Int64 }

func (m *meter) Enabled(context.Context, slog.Level) bool { return true }
func (m *meter) WithAttrs([]slog.Attr) slog.Handler       { return m }
func (m *meter) WithGroup(string) slog.Handler            { return m }

func (m *meter) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "claude call" {
		return nil
	}
	r.Attrs(func(a slog.Attr) bool {
		var n int64
		switch v := a.Value; v.Kind() {
		case slog.KindInt64:
			n = v.Int64()
		case slog.KindUint64:
			n = int64(v.Uint64())
		}
		switch a.Key {
		case "input_tokens":
			m.in.Add(n)
		case "output_tokens":
			m.out.Add(n)
		}
		return true
	})
	return nil
}

func (m *meter) usd(p prices) float64 {
	return (float64(m.in.Load())*p.input + float64(m.out.Load())*p.output) / 1e6
}

// save writes the run as <UTC time>_<model>_<prompt version>.json.
func save(dir string, r interpreteval.Run) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s_%s_%s.json", r.StartedAt.Format("20060102T150405Z"), r.Model, r.PromptVersion)
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, append(b, '\n'), 0o644)
}

func compare(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: interpret-eval compare A.json B.json")
	}
	var runs [2]interpreteval.Run
	for i, path := range args {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &runs[i]); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	fmt.Print(interpreteval.Compare(runs[0], runs[1]))
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
