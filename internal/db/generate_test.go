package db

import (
	"reflect"
	"testing"
)

func TestGenerate_EveryProfileHasAtLeast50Users(t *testing.T) {
	t.Parallel() // pure function; nothing shared with other tests
	data := generate(20261021)

	perProfile := map[string]int{}
	for _, u := range data.users {
		perProfile[u.profile]++
	}
	for _, p := range profiles {
		if perProfile[p.name] < 50 {
			t.Errorf("profile %s: %d users, want >= 50", p.name, perProfile[p.name])
		}
	}
	if len(data.users) != 3000 {
		t.Fatalf("want 3000 users, got %d", len(data.users))
	}
}

func TestGenerate_SameSeedSameData(t *testing.T) {
	t.Parallel() // pure function; nothing shared with other tests
	if !reflect.DeepEqual(generate(1), generate(1)) {
		t.Fatal("same seed produced different data")
	}
	if reflect.DeepEqual(generate(1), generate(2)) {
		t.Fatal("different seeds produced identical data")
	}
}

func TestEmailLocalPart_FoldsTurkishLetters(t *testing.T) {
	t.Parallel() // pure function; nothing shared with other tests
	if got := emailLocalPart("Çağrı", "Işık Öztürk"); got != "cagri.isik.ozturk" {
		t.Fatalf("got %q", got)
	}
	if got := emailLocalPart("İpek", ""); got != "ipek" {
		t.Fatalf("got %q", got)
	}
}
