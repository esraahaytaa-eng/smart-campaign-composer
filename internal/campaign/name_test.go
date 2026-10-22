package campaign

import (
	"strings"
	"testing"
)

func TestDefaultName(t *testing.T) {
	for _, tc := range []struct{ brief, want string }{
		{"Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00.", "Send a weekend sale announcement to all users in Ankara."},
		{"Toplamda en az 10.000 TL harcayan müşterilere VIP teşekkür hediyesi gönder.", "Toplamda en az 10.000 TL harcayan müşterilere VIP teşekkür…"},
		{"Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything.", "Send a 30% discount reminder to users in Istanbul who added…"},
		{"  Win back customers\nwho left  ", "Win back customers"},
		{"Kısa bir duyuru", "Kısa bir duyuru"},
		{strings.Repeat("ş", 70), strings.Repeat("ş", 59) + "…"},
		{"   ", "Untitled campaign"},
	} {
		got := defaultName(tc.brief)
		if got != tc.want {
			t.Errorf("defaultName(%q)\n got %q\nwant %q", tc.brief, got, tc.want)
		}
		if n := len([]rune(got)); n > 60 {
			t.Errorf("defaultName(%q) is %d characters, want at most 60", tc.brief, n)
		}
	}
}

func TestResendName(t *testing.T) {
	if got := resendName("Ankara weekend sale", "sms"); got != "Ankara weekend sale · resend (SMS)" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("a", 100)
	if got := resendName(long, "email"); len([]rune(got)) > maxNameChars || !strings.HasSuffix(got, "· resend (Email)") {
		t.Fatalf("a resend of a long name must stay within %d characters and keep the suffix, got %q", maxNameChars, got)
	}
}
