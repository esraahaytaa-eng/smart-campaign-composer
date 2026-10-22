package llm

import (
	"encoding/json"
	"reflect"
	"testing"
	"unicode/utf8"
)

var contentFields = []string{"sms.body", "email.subject", "email.body"}

// A content answer with every tricky case: escaped quotes and backslashes,
// \n, \u escapes (incl. a surrogate pair), raw Turkish letters, braces and
// a field the streamer must ignore.
const trickyJSON = `{"sms": {"body": "Merhaba {{first_name}}! \"%30\" İndirim\\yol\nŞimdi \u00e7ok \ud83d\ude00 güzel"},` +
	` "note": "ignore me", "email": {"subject": "İSTANBUL \u0130 {sale}", "body": "Satır 1\r\nSatır 2\t\/son"}}`

// collect streams the chunks and joins the deltas per field.
func collect(chunks []string) (map[string]string, []Delta) {
	got := map[string]string{}
	var deltas []Delta
	s := NewFieldStreamer(contentFields, func(d Delta) {
		got[d.Field] += d.Text
		deltas = append(deltas, d)
	})
	for _, c := range chunks {
		s.Write(c)
	}
	return got, deltas
}

func wantFields(t *testing.T, raw string) map[string]string {
	t.Helper()
	var v struct {
		SMS   struct{ Body string } `json:"sms"`
		Email struct {
			Subject string
			Body    string
		} `json:"email"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"sms.body": v.SMS.Body, "email.subject": v.Email.Subject, "email.body": v.Email.Body}
}

func TestFieldStreamer_EverySplitPointMatchesJSONDecode(t *testing.T) {
	want := wantFields(t, trickyJSON)

	for i := 0; i <= len(trickyJSON); i++ { // byte positions: splits runes and escapes
		got, deltas := collect([]string{trickyJSON[:i], trickyJSON[i:]})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("split at byte %d:\n got %q\nwant %q", i, got, want)
		}
		for _, d := range deltas {
			if !utf8.ValidString(d.Text) {
				t.Fatalf("split at byte %d: delta %q is not valid UTF-8", i, d.Text)
			}
		}
	}
}

func TestFieldStreamer_ByteByByte(t *testing.T) {
	var chunks []string
	for i := range len(trickyJSON) {
		chunks = append(chunks, trickyJSON[i:i+1])
	}
	got, deltas := collect(chunks)

	if want := wantFields(t, trickyJSON); !reflect.DeepEqual(got, want) {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	for _, d := range deltas {
		if !utf8.ValidString(d.Text) {
			t.Fatalf("delta %q is not valid UTF-8", d.Text)
		}
	}
}

func TestFieldStreamer_DeltasInStreamOrder(t *testing.T) {
	_, deltas := collect([]string{`{"sms":{"body":"ab`, `c"},"email":{"subject":"S","body":"B1`, `B2"}}`})

	var order []string
	for _, d := range deltas {
		if len(order) == 0 || order[len(order)-1] != d.Field {
			order = append(order, d.Field)
		}
	}
	if !reflect.DeepEqual(order, []string{"sms.body", "email.subject", "email.body"}) {
		t.Fatalf("field order %v (deltas %+v)", order, deltas)
	}
}

func TestFieldStreamer_IgnoresOtherFieldsAndNesting(t *testing.T) {
	got, _ := collect([]string{`{"note":"x","meta":{"body":"no"},"list":["a",{"body":"no"}],"sms":{"body":"yes","extra":"no"},"n":1.5,"b":true}`})

	if !reflect.DeepEqual(got, map[string]string{"sms.body": "yes"}) {
		t.Fatalf("got %q", got)
	}
}
