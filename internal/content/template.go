// Package content validates and personalizes message templates (D2, D3,
// D12, D13). Templates come from Claude or the marketer and are untrusted.
package content

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Limits in characters (Unicode code points, D2), checked on the personalized text.
const (
	SMSLimit       = 160
	SubjectLimit   = 80
	EmailBodyLimit = 2000
)

// Var is one allowed personalization variable (D12).
type Var string

const (
	FirstName Var = "first_name"
	LastName  Var = "last_name"
	City      Var = "city"
)

// renderOrder is the fixed substitution order, shared with the SQL estimate
// (estimate.go) so both produce exactly the same text.
var renderOrder = []Var{FirstName, LastName, City}

// UserVars are one recipient's values; an empty value renders as "".
type UserVars struct {
	FirstName, LastName, City string
}

func (u UserVars) value(v Var) string {
	switch v {
	case FirstName:
		return u.FirstName
	case LastName:
		return u.LastName
	default:
		return u.City
	}
}

var (
	varToken   = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
	spaceRuns  = regexp.MustCompile(` {2,}`)
	errSyntax  = errors.New("braces may only be used for {{first_name}}, {{last_name}} or {{city}}")
	allowedVar = map[string]Var{"first_name": FirstName, "last_name": LastName, "city": City}
)

// Parse returns the variables a template uses, in order of first use. Only the
// exact tokens {{first_name}}, {{last_name}} and {{city}} are allowed; any
// other use of { or } is an error, so typos never reach a recipient.
func Parse(tpl string) ([]Var, error) {
	var vars []Var
	for _, m := range varToken.FindAllStringSubmatch(tpl, -1) {
		v, ok := allowedVar[m[1]]
		if !ok {
			return nil, fmt.Errorf("unknown variable {{%s}}: use {{first_name}}, {{last_name}} or {{city}}", m[1])
		}
		if !slices.Contains(vars, v) {
			vars = append(vars, v)
		}
	}
	if strings.ContainsAny(varToken.ReplaceAllString(tpl, ""), "{}") {
		return nil, errSyntax
	}
	return vars, nil
}

// Render personalizes a valid template: variables are replaced in a fixed
// order, runs of spaces left by empty values collapse to one, and leading and
// trailing spaces are trimmed. Line breaks are kept (D13).
func Render(tpl string, u UserVars) string {
	s := tpl
	for _, v := range renderOrder {
		s = strings.ReplaceAll(s, "{{"+string(v)+"}}", u.value(v))
	}
	return strings.Trim(spaceRuns.ReplaceAllString(s, " "), " ")
}

// Message is one channel's template. SMS has no subject.
type Message struct {
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

// Content holds a template per chosen channel.
type Content struct {
	SMS   *Message `json:"sms,omitempty"`
	Email *Message `json:"email,omitempty"`
}

// FieldError names the invalid field: "sms", "sms.body", "email.subject", ...
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidateDraft checks content before approval: exactly the chosen channels,
// valid variables, a single-line subject, and fixed text (every variable
// empty) within the limits. Over-limit messages for particular recipients are
// only an estimate here (D3); the send-time check decides.
func ValidateDraft(c Content, channels []string) []FieldError {
	var errs []FieldError
	for ch, m := range map[string]*Message{"sms": c.SMS, "email": c.Email} {
		chosen := slices.Contains(channels, ch)
		switch {
		case chosen && m == nil:
			errs = append(errs, FieldError{ch, "content is missing for a chosen channel"})
		case !chosen && m != nil:
			errs = append(errs, FieldError{ch, "content given for a channel that was not chosen"})
		}
	}
	if c.SMS != nil && slices.Contains(channels, "sms") {
		if c.SMS.Subject != "" {
			errs = append(errs, FieldError{"sms.subject", "SMS messages have no subject"})
		}
		errs = appendIf(errs, "sms.body", checkTemplate(c.SMS.Body, SMSLimit))
	}
	if c.Email != nil && slices.Contains(channels, "email") {
		err := checkTemplate(c.Email.Subject, SubjectLimit)
		if err == nil && strings.ContainsAny(c.Email.Subject, "\r\n") {
			err = errors.New("the subject must be a single line")
		}
		errs = appendIf(errs, "email.subject", err)
		errs = appendIf(errs, "email.body", checkTemplate(c.Email.Body, EmailBodyLimit))
	}
	slices.SortFunc(errs, func(a, b FieldError) int { return strings.Compare(a.Field, b.Field) })
	return errs
}

func checkTemplate(tpl string, limit int) error {
	if strings.TrimSpace(tpl) == "" {
		return errors.New("must not be empty")
	}
	// Variables make text longer when rendered, never this much: the cap only
	// bounds untrusted input.
	if utf8.RuneCountInString(tpl) > 2*limit {
		return fmt.Errorf("the template is longer than %d characters", 2*limit)
	}
	if _, err := Parse(tpl); err != nil {
		return err
	}
	if n := utf8.RuneCountInString(Render(tpl, UserVars{})); n > limit {
		return fmt.Errorf("the fixed text alone is %d characters; the limit is %d, so no recipient could receive it", n, limit)
	}
	return nil
}

func appendIf(errs []FieldError, field string, err error) []FieldError {
	if err != nil {
		errs = append(errs, FieldError{field, err.Error()})
	}
	return errs
}
