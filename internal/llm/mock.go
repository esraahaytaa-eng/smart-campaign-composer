package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"smart-campaign-composer/internal/audience"
)

// MockUnsupportedReason is the mock's answer to any brief it does not fully understand.
const MockUnsupportedReason = "Mock AI only understands the example briefs listed in the README. " +
	"Use LLM_MODE=real for free-form requests, or build the rules by hand."

// Mock understands a fixed set of brief patterns and nothing else (D15, D24).
// A pattern must match the whole first sentence; every further sentence must
// be a tone or schedule phrase. Anything else is "unsupported": the mock never
// guesses, drops or broadens a condition.
type Mock struct {
	ChunkDelay time.Duration // pause between streamed content chunks, so the UI shows streaming
}

func NewMock() *Mock { return &Mock{} }

// MockExample is one documented brief and the rules the mock returns for it.
// The README lists the same briefs (TestMock_ExamplesAreInREADME).
type MockExample struct{ Brief, Rules string }

var MockExamples = []MockExample{
	{"Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything. Keep the message short and friendly.",
		`[{"field":"city","op":"in","value":["İstanbul"]},{"field":"cart_added","op":"within","value":{"last_days":30}},{"field":"no_order","op":"within","value":{"last_days":30}}]`},
	{"Send a we-miss-you coupon to customers whose last order was more than 90 days ago.",
		`[{"field":"last_order","op":"before","value":{"last_days":90}}]`},
	{"Send a VIP thank-you gift to customers who spent at least 10,000 TL in total.",
		`[{"field":"total_spend","op":"gte","value":10000}]`},
	{"Send a new arrivals note to customers who bought books in the last 30 days.",
		`[{"field":"ordered_category","op":"in","value":{"categories":["books"],"last_days":30}}]`},
	{"Send a free shipping offer to users with at least 5,000 TL in their cart.",
		`[{"field":"cart_value","op":"gte","value":5000}]`},
	{"Send a welcome discount to users who signed up in the last 7 days.",
		`[{"field":"signed_up","op":"within","value":{"last_days":7}}]`},
	{"Send a loyalty reward to customers who spent more than 10,000 TL in the last 12 months with at least 4 orders, no orders in the last 45 days, and who did not sign up in the last 7 days.",
		loyalRules},
	{"Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00.",
		`[{"field":"city","op":"in","value":["Ankara"]}]`},
	{"Send a reminder to users who added products to their cart since 2026-10-01 but did not buy anything.",
		`[{"field":"cart_added","op":"within","value":{"from":"2026-10-01"}},{"field":"no_order","op":"within","value":{"from":"2026-10-01"}}]`},
	{"İstanbul'da son 30 günde sepetine ürün ekleyip satın almayan kullanıcılara %30 indirim hatırlatması gönder. Mesaj kısa ve samimi olsun.",
		`[{"field":"city","op":"in","value":["İstanbul"]},{"field":"cart_added","op":"within","value":{"last_days":30}},{"field":"no_order","op":"within","value":{"last_days":30}}]`},
	{"Son siparişi 90 günden eski olan müşterilere seni özledik kuponu gönder.",
		`[{"field":"last_order","op":"before","value":{"last_days":90}}]`},
	{"Toplamda en az 10.000 TL harcayan müşterilere VIP teşekkür hediyesi gönder.",
		`[{"field":"total_spend","op":"gte","value":10000}]`},
	{"Son 30 günde kitap kategorisinden alışveriş yapan müşterilere yeni ürün duyurusu gönder.",
		`[{"field":"ordered_category","op":"in","value":{"categories":["books"],"last_days":30}}]`},
	{"Sepetinde en az 5.000 TL ürün olan kullanıcılara ücretsiz kargo fırsatı gönder.",
		`[{"field":"cart_value","op":"gte","value":5000}]`},
	{"Son 7 günde üye olan kullanıcılara hoş geldin indirimi gönder.",
		`[{"field":"signed_up","op":"within","value":{"last_days":7}}]`},
	{"Son 12 ayda 10.000 TL'den fazla harcayan, bu sürede en az 4 sipariş veren, son 45 gündür sipariş vermeyen ve son 7 günde üye olmamış müşterilere sadakat ödülü gönder.",
		loyalRules},
	{"Ankara'daki tüm kullanıcılara hafta sonu indirimi duyurusu gönder. Yarın saat 10:00'da gönder.",
		`[{"field":"city","op":"in","value":["Ankara"]}]`},
	{"2026-10-01 tarihinden beri sepetine ürün ekleyip satın almayan kullanıcılara hatırlatma gönder.",
		`[{"field":"cart_added","op":"within","value":{"from":"2026-10-01"}},{"field":"no_order","op":"within","value":{"from":"2026-10-01"}}]`},
}

// loyalRules: over 10,000 TL and 4+ orders in 12 months, no order in 45 days, not new (D70).
const loyalRules = `[{"field":"spend_in_window","op":"gt","value":{"amount":10000,"last_months":12}},{"field":"orders_in_window","op":"gte","value":{"count":4,"last_months":12}},{"field":"no_order","op":"within","value":{"last_days":45}},{"field":"signed_up","op":"before","value":{"last_days":7}}]`

// slots are the variable parts of a pattern.
var slots = strings.NewReplacer(
	// The offer is a short phrase ("30% discount reminder"): at most 4 words,
	// so it cannot carry a hidden audience condition.
	"{offer}", `(?P<offer>(?:%?\d{1,3}%? )?[\pL'’-]+(?: [\pL'’-]+){0,3})`,
	"{city}", `(?P<city>\pL+(?: \pL+)?)`,
	"{n}", `(?P<n>\d{1,3})`,
	"{amount}", `(?P<amount>\d{1,3}(?:[.,]\d{3})+|\d{1,7})`,
	"{category}", `(?P<category>\pL+)`,
	"{date}", `(?P<date>\d{4}-\d{2}-\d{2})`,
	"{months}", `(?P<months>\d{1,2})`,
	"{count}", `(?P<count>\d{1,4})`,
	"{days}", `(?P<days>\d{1,3})`,
	"{'}", `['’]`,
)

type pattern struct {
	re    *regexp.Regexp
	rules func(m map[string]string) (string, error)
}

func p(expr string, rules func(m map[string]string) (string, error)) pattern {
	return pattern{regexp.MustCompile(`(?i)^` + slots.Replace(expr) + `$`), rules}
}

var patterns = []pattern{
	p(`send (?:a |an )?{offer} to (?:users|customers) in {city} who added (?:products|items) to their (?:cart|basket) in the last {n} days but did not buy anything`, cartAbandoners),
	p(`{city}{'}(?:da|de|ta|te) son {n} günde sepetine ürün ekleyip satın almayan (?:kullanıcılara|müşterilere) {offer} gönder`, cartAbandoners),
	p(`send (?:a |an )?{offer} to customers whose last order was more than {n} days ago`, lastOrderBefore),
	p(`son siparişi {n} günden eski olan müşterilere {offer} gönder`, lastOrderBefore),
	p(`send (?:a |an )?{offer} to customers who spent at least {amount} (?:tl|try|lira)(?: in total)?`, amountRule("total_spend")),
	p(`toplamda en az {amount} (?:tl|lira) harcayan müşterilere {offer} gönder`, amountRule("total_spend")),
	p(`send (?:a |an )?{offer} to customers who bought {category} in the last {n} days`, orderedCategory),
	p(`son {n} günde {category} (?:kategorisinden )?alışveriş yapan müşterilere {offer} gönder`, orderedCategory),
	p(`send (?:a |an )?{offer} to (?:users|customers) with at least {amount} (?:tl|try|lira) in their (?:cart|basket)`, amountRule("cart_value")),
	p(`sepetinde en az {amount} (?:tl|lira)(?:lık)? ürün olan (?:kullanıcılara|müşterilere) {offer} gönder`, amountRule("cart_value")),
	p(`send (?:a |an )?{offer} to (?:users|customers) who signed up in the last {n} days`, signedUp),
	p(`son {n} günde üye olan (?:kullanıcılara|müşterilere) {offer} gönder`, signedUp),
	p(`send (?:a |an )?{offer} to (?:all )?(?:users|customers) in {city}`, cityOnly),
	p(`{city}{'}(?:daki|deki|taki|teki) (?:tüm )?(?:kullanıcılara|müşterilere) {offer} gönder`, cityOnly),
	p(`send (?:a |an )?{offer} to (?:users|customers) who added (?:products|items) to their (?:cart|basket) since {date} but did not buy anything`, cartSince),
	p(`{date} tarihinden beri sepetine ürün ekleyip satın almayan (?:kullanıcılara|müşterilere) {offer} gönder`, cartSince),
	p(`send (?:a |an )?{offer} to customers who spent more than {amount} (?:tl|try|lira) in the last {months} months with at least {count} orders, no orders in the last {n} days,? and who did not sign up in the last {days} days`, loyal),
	p(`son {months} ayda {amount} (?:tl|lira){'}den fazla harcayan, bu sürede en az {count} sipariş veren, son {n} gündür sipariş vermeyen ve son {days} günde üye olmamış müşterilere {offer} gönder`, loyal),
}

// loyal: more than the amount and at least count orders in the months, no
// order in n days, not signed up in the last days (D70).
func loyal(m map[string]string) (string, error) {
	amount := strings.NewReplacer(".", "", ",", "").Replace(m["amount"])
	return fmt.Sprintf(`[{"field":"spend_in_window","op":"gt","value":{"amount":%[1]s,"last_months":%[2]s}},`+
		`{"field":"orders_in_window","op":"gte","value":{"count":%[3]s,"last_months":%[2]s}},`+
		`{"field":"no_order","op":"within","value":{"last_days":%[4]s}},`+
		`{"field":"signed_up","op":"before","value":{"last_days":%[5]s}}]`, amount, m["months"], m["count"], m["n"], m["days"]), nil
}

func cartAbandoners(m map[string]string) (string, error) {
	return fmt.Sprintf(`[{"field":"city","op":"in","value":[%q]},{"field":"cart_added","op":"within","value":{"last_days":%s}},{"field":"no_order","op":"within","value":{"last_days":%[2]s}}]`, m["city"], m["n"]), nil
}

func lastOrderBefore(m map[string]string) (string, error) {
	return fmt.Sprintf(`[{"field":"last_order","op":"before","value":{"last_days":%s}}]`, m["n"]), nil
}

func amountRule(field string) func(map[string]string) (string, error) {
	return func(m map[string]string) (string, error) {
		amount := strings.NewReplacer(".", "", ",", "").Replace(m["amount"])
		return fmt.Sprintf(`[{"field":%q,"op":"gte","value":%s}]`, field, amount), nil
	}
}

// Category names the mock accepts, English codes and Turkish names.
var categoryNames = map[string]string{
	"electronics": "electronics", "elektronik": "electronics",
	"fashion": "fashion", "moda": "fashion", "giyim": "fashion",
	"home": "home", "ev": "home",
	"beauty": "beauty", "kozmetik": "beauty",
	"sports": "sports", "spor": "sports",
	"books": "books", "kitap": "books",
}

func orderedCategory(m map[string]string) (string, error) {
	code, ok := categoryNames[strings.ToLower(m["category"])]
	if !ok {
		return "", fmt.Errorf("unknown category %q", m["category"])
	}
	return fmt.Sprintf(`[{"field":"ordered_category","op":"in","value":{"categories":[%q],"last_days":%s}}]`, code, m["n"]), nil
}

func signedUp(m map[string]string) (string, error) {
	return fmt.Sprintf(`[{"field":"signed_up","op":"within","value":{"last_days":%s}}]`, m["n"]), nil
}

func cityOnly(m map[string]string) (string, error) {
	return fmt.Sprintf(`[{"field":"city","op":"in","value":[%q]}]`, m["city"]), nil
}

func cartSince(m map[string]string) (string, error) {
	return fmt.Sprintf(`[{"field":"cart_added","op":"within","value":{"from":%q}},{"field":"no_order","op":"within","value":{"from":%[1]q}}]`, m["date"]), nil
}

// offerWords may not appear in the offer: they name people or start a
// condition ("only for women", "VIP hariç"). Matched per word, in lower case.
var offerWords = map[string]bool{
	"who": true, "whose": true, "which": true, "that": true, "user": true, "users": true, "customer": true,
	"customers": true, "people": true, "members": true, "everyone": true, "everybody": true, "all": true,
	"for": true, "to": true, "in": true, "only": true, "except": true, "and": true, "or": true, "with": true,
	"without": true, "not": true, "but": true, "if": true, "unless": true,
	"olan": true, "yapan": true, "eden": true, "için": true, "sadece": true, "yalnızca": true, "hariç": true,
	"ve": true, "veya": true, "ya": true, "ile": true, "değil": true, "tüm": true, "herkes": true, "herkese": true,
}

func offerHasCondition(offer string) bool {
	for _, w := range strings.Fields(strings.ToLower(offer)) {
		if offerWords[w] || strings.HasPrefix(w, "kullanıcı") || strings.HasPrefix(w, "müşteri") {
			return true
		}
	}
	return false
}

var (
	sentenceEnd = regexp.MustCompile(`[.!?]+\s+`)
	toneRe      = regexp.MustCompile(`(?i)^(?:keep (?:the messages?|it) (?:short|friendly|formal|casual|fun)(?:(?:,| and) (?:short|friendly|formal|casual|fun))*|mesaj(?:lar)?ı? (?:kısa|samimi|resmi|eğlenceli)(?:(?:,| ve) (?:kısa|samimi|resmi|eğlenceli))* olsun)$`)
	atTimeRe    = regexp.MustCompile(`(?i)^(?:(?:send it )?(?P<day>tomorrow|today) at (?P<h>\d{1,2})(?::(?P<m>\d{2}))?|(?P<day>yarın|bugün) saat (?P<h>\d{1,2})(?::(?P<m>\d{2}))?(?:['’](?:da|de|ta|te))? gönder)$`)
	inHoursRe   = regexp.MustCompile(`(?i)^(?:(?:send it )?in (?P<hours>[1-9]\d?) hours?|(?P<hours>[1-9]\d?) saat sonra gönder)$`)
)

func (m *Mock) InterpretRequest(ctx context.Context, in InterpretInput) (json.RawMessage, error) {
	loc, err := time.LoadLocation(in.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("mock: time zone: %w", err)
	}
	sentences := sentenceEnd.Split(strings.TrimRight(strings.TrimSpace(in.Text), ".!? "), -1)

	rulesJSON, ok := matchCore(sentences[0])
	if !ok {
		return unsupported(MockUnsupportedReason), nil
	}
	out := interpretOutput{}
	for _, s := range sentences[1:] {
		switch {
		case toneRe.MatchString(s):
		case out.ScheduleNote == nil:
			at, ok := scheduleAt(s, in.Now, loc)
			if !ok {
				return unsupported(MockUnsupportedReason), nil
			}
			formatted, note := at.Format(time.RFC3339), s
			out.SuggestedSendAt, out.ScheduleNote = &formatted, &note
		default: // a second schedule phrase
			return unsupported(MockUnsupportedReason), nil
		}
	}

	if err := json.Unmarshal([]byte(rulesJSON), &out.Rules); err != nil {
		return nil, fmt.Errorf("mock: %w", err)
	}
	// Return only rules that pass validation, in canonical form; otherwise say so.
	rules, errs := audience.Validate(out.Rules, in.Catalog)
	if len(errs) > 0 {
		return unsupported("Mock AI: " + errs[0].Message + ". " + MockUnsupportedReason), nil
	}
	out.Rules = rules
	return json.Marshal(out)
}

// matchCore returns the rules of the pattern that matches the whole sentence.
func matchCore(sentence string) (string, bool) {
	for _, pat := range patterns {
		match := pat.re.FindStringSubmatch(sentence)
		if match == nil {
			continue
		}
		slots := map[string]string{}
		for i, name := range pat.re.SubexpNames() {
			if name != "" && match[i] != "" {
				slots[name] = match[i]
			}
		}
		if offerHasCondition(slots["offer"]) {
			return "", false
		}
		rules, err := pat.rules(slots)
		return rules, err == nil
	}
	return "", false
}

// scheduleAt reads "tomorrow at 10:00", "yarın saat 10:00'da gönder",
// "in 2 hours" or "2 saat sonra gönder" relative to now in loc.
func scheduleAt(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	if m := named(inHoursRe, s); m != nil {
		h, _ := strconv.Atoi(m["hours"])
		return now.In(loc).Add(time.Duration(h) * time.Hour), true
	}
	m := named(atTimeRe, s)
	if m == nil {
		return time.Time{}, false
	}
	h, _ := strconv.Atoi(m["h"])
	minute := 0
	if m["m"] != "" {
		minute, _ = strconv.Atoi(m["m"])
	}
	if h > 23 || minute > 59 {
		return time.Time{}, false
	}
	day := now.In(loc)
	if d := strings.ToLower(m["day"]); d == "tomorrow" || d == "yarın" {
		day = day.AddDate(0, 0, 1)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), h, minute, 0, 0, loc), true
}

// named returns the non-empty named groups of a full match, or nil.
func named(re *regexp.Regexp, s string) map[string]string {
	match := re.FindStringSubmatch(s)
	if match == nil {
		return nil
	}
	out := map[string]string{}
	for i, name := range re.SubexpNames() {
		if name != "" && match[i] != "" {
			out[name] = match[i]
		}
	}
	return out
}
