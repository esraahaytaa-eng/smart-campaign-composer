package content

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParse_Variables(t *testing.T) {
	vars, err := Parse("Hi {{first_name}} {{last_name}}, {{city}} deals! Bye {{first_name}}.")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(vars, []Var{FirstName, LastName, City}) {
		t.Fatalf("got %v", vars)
	}
	if vars, err := Parse("No variables here."); err != nil || len(vars) != 0 {
		t.Fatalf("plain text: %v %v", vars, err)
	}
}

func TestParse_RejectsMalformed(t *testing.T) {
	for _, tpl := range []string{
		"Hi {{ first_name }}",
		"Hi {{FIRST_NAME}}",
		"Hi {{email}}",
		"Hi {first_name}",
		"Hi {{first_name",
		"Hi first_name}}",
		"Hi {{first_name}}}",
		"Hi {{{first_name}}",
		"Use code {SALE}",
		"Hi {{}}",
	} {
		if _, err := Parse(tpl); err == nil {
			t.Errorf("%q: want error", tpl)
		}
	}
}

func TestRender_TurkishLengthInCharacters(t *testing.T) {
	got := Render("Merhaba {{first_name}}, {{city}}'da %30 indirim!", UserVars{FirstName: "Şükrü", City: "İstanbul"})

	if got != "Merhaba Şükrü, İstanbul'da %30 indirim!" {
		t.Fatalf("got %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 39 {
		t.Fatalf("want 39 characters (code points), got %d", n)
	}
}

func TestRender_EmptyValuesAreTidied(t *testing.T) {
	cases := []struct {
		tpl  string
		u    UserVars
		want string
	}{
		// Only spaces are tidied; "Ali ," reads awkwardly, as D12 accepts.
		{"Dear {{first_name}} {{last_name}}, hi", UserVars{FirstName: "Ali"}, "Dear Ali , hi"},
		{"{{last_name}} {{first_name}} welcome", UserVars{FirstName: "Ali"}, "Ali welcome"},
		{"Hi  there   {{city}}  ", UserVars{}, "Hi there"},
		// Line breaks are kept (D13): only spaces are collapsed and trimmed.
		{"Hi {{first_name}}\n\nLine two", UserVars{}, "Hi \n\nLine two"},
	}
	for _, tc := range cases {
		if got := Render(tc.tpl, tc.u); got != tc.want {
			t.Errorf("Render(%q) = %q, want %q", tc.tpl, got, tc.want)
		}
	}
}

func sms(body string) Content { return Content{SMS: &Message{Body: body}} }

func fieldErrs(errs []FieldError) string {
	var parts []string
	for _, e := range errs {
		parts = append(parts, e.Field+": "+e.Message)
	}
	return strings.Join(parts, "; ")
}

func TestValidateDraft_FixedTextOverLimit(t *testing.T) {
	if errs := ValidateDraft(sms(strings.Repeat("a", 172)), []string{"sms"}); len(errs) != 1 || errs[0].Field != "sms.body" {
		t.Fatalf("172 fixed characters: want one sms.body error, got %s", fieldErrs(errs))
	}
	if errs := ValidateDraft(sms(strings.Repeat("a", 150)+" {{first_name}}"), []string{"sms"}); len(errs) != 0 {
		t.Fatalf("150 characters + {{first_name}}: want valid, got %s", fieldErrs(errs))
	}
	// Turkish letters count as one character each (D2).
	if errs := ValidateDraft(sms(strings.Repeat("ş", 160)), []string{"sms"}); len(errs) != 0 {
		t.Fatalf("160 × ş: want valid, got %s", fieldErrs(errs))
	}
}

func TestValidateDraft_Email(t *testing.T) {
	email := func(subject, body string) Content { return Content{Email: &Message{Subject: subject, Body: body}} }
	cases := []struct {
		name  string
		c     Content
		field string
	}{
		{"subject over 80", email(strings.Repeat("s", 81), "Body"), "email.subject"},
		{"subject with newline", email("Line one\nLine two", "Body"), "email.subject"},
		{"empty subject", email("  ", "Body"), "email.subject"},
		{"body over 2000", email("Subject", strings.Repeat("b", 2001)), "email.body"},
		{"empty body", email("Subject", ""), "email.body"},
		{"bad variable in subject", email("Hi {{email}}", "Body"), "email.subject"},
		{"raw template too long", email("Subject", strings.Repeat("{{city}}", 600)), "email.body"},
	}
	for _, tc := range cases {
		errs := ValidateDraft(tc.c, []string{"email"})
		if len(errs) != 1 || errs[0].Field != tc.field {
			t.Errorf("%s: want one %s error, got %s", tc.name, tc.field, fieldErrs(errs))
		}
	}
	ok := email("Hi {{first_name}}", "Hello {{first_name}},\n\nOur sale in {{city}} starts today.\n")
	if errs := ValidateDraft(ok, []string{"email"}); len(errs) != 0 {
		t.Fatalf("valid email: %s", fieldErrs(errs))
	}
}

func TestValidateDraft_ChannelsMatchContent(t *testing.T) {
	both := Content{SMS: &Message{Body: "Hi"}, Email: &Message{Subject: "S", Body: "B"}}

	if errs := ValidateDraft(sms("Hi"), []string{"sms", "email"}); len(errs) != 1 || errs[0].Field != "email" {
		t.Errorf("missing email content: got %s", fieldErrs(errs))
	}
	if errs := ValidateDraft(both, []string{"sms"}); len(errs) != 1 || errs[0].Field != "email" {
		t.Errorf("content for an unchosen channel: got %s", fieldErrs(errs))
	}
	if errs := ValidateDraft(Content{SMS: &Message{Subject: "x", Body: "Hi"}}, []string{"sms"}); len(errs) != 1 || errs[0].Field != "sms.subject" {
		t.Errorf("SMS with a subject: got %s", fieldErrs(errs))
	}
	if errs := ValidateDraft(both, []string{"sms", "email"}); len(errs) != 0 {
		t.Errorf("valid content: got %s", fieldErrs(errs))
	}
}

// A template of variables only renders empty for recipients without those
// values (Milestone C review), so some fixed text is required.
func TestValidateDraft_RequiresFixedText(t *testing.T) {
	for name, c := range map[string]Content{
		"sms of one variable":   {SMS: &Message{Body: "{{last_name}}"}},
		"sms of variables only": {SMS: &Message{Body: "{{first_name}}  {{city}}"}},
	} {
		if errs := ValidateDraft(c, []string{"sms"}); len(errs) != 1 || errs[0].Field != "sms.body" {
			t.Errorf("%s: want one sms.body error, got %s", name, fieldErrs(errs))
		}
	}
	subjectOnlyVar := Content{Email: &Message{Subject: "{{first_name}}", Body: "Hello"}}
	if errs := ValidateDraft(subjectOnlyVar, []string{"email"}); len(errs) != 1 || errs[0].Field != "email.subject" {
		t.Errorf("subject of one variable: want one email.subject error, got %s", fieldErrs(errs))
	}
	if errs := ValidateDraft(sms("{{first_name}}!"), []string{"sms"}); len(errs) != 0 {
		t.Errorf("one character of fixed text is enough: got %s", fieldErrs(errs))
	}
}
