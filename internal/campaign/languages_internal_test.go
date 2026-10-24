package campaign

import (
	"reflect"
	"slices"
	"testing"

	"smart-campaign-composer/internal/audience"
)

func TestRecommend_ThresholdAndDefault(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		by    map[string]audience.ChannelCounts
		langs []string
		def   string
	}{
		{"both above 5%", map[string]audience.ChannelCounts{"tr": {Total: 90}, "en": {Total: 10}}, []string{"tr", "en"}, "tr"},
		{"English the largest", map[string]audience.ChannelCounts{"tr": {Total: 30}, "en": {Total: 70}}, []string{"tr", "en"}, "en"},
		{"4% is not recommended", map[string]audience.ChannelCounts{"tr": {Total: 96}, "en": {Total: 4}}, []string{"tr"}, "tr"},
		{"exactly 5% is", map[string]audience.ChannelCounts{"tr": {Total: 95}, "en": {Total: 5}}, []string{"tr", "en"}, "tr"},
		{"a tie picks the catalog order", map[string]audience.ChannelCounts{"en": {Total: 50}, "tr": {Total: 50}}, []string{"tr", "en"}, "tr"},
		{"nobody", nil, nil, ""},
	} {
		langs, def := recommend(audience.Counts{ByLanguage: tc.by})
		if !slices.Equal(langs, tc.langs) || def != tc.def {
			t.Errorf("%s: want %v default %q, got %v default %q", tc.name, tc.langs, tc.def, langs, def)
		}
	}
}

// Review focus 4: recipients whose language has no content get the default
// and are counted under it as fallback.
func TestDelivery_FallbackCountedUnderDefault(t *testing.T) {
	t.Parallel()
	counts := audience.Counts{ByLanguage: map[string]audience.ChannelCounts{
		"tr": {Total: 9, SMS: 8, Email: 5}, "en": {Total: 3, SMS: 2, Email: 3},
	}}

	got := delivery(counts, []string{"sms", "email"}, []string{"tr"}, "tr")
	want := map[string]map[string]deliveryCount{
		"sms":   {"tr": {Own: 8, Fallback: 2}},
		"email": {"tr": {Own: 5, Fallback: 3}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Turkish only: want %+v, got %+v", want, got)
	}

	got = delivery(counts, []string{"sms"}, []string{"tr", "en"}, "en")
	want = map[string]map[string]deliveryCount{"sms": {"tr": {Own: 8}, "en": {Own: 2}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("both languages: want %+v, got %+v", want, got)
	}

	got = delivery(counts, []string{"sms"}, []string{"und"}, "und")
	want = map[string]map[string]deliveryCount{"sms": {"und": {Fallback: 10}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy content reaches everyone: want %+v, got %+v", want, got)
	}
}
