package usage

import (
	"math"
	"testing"
)

func pricingRecord(model, tier string, input, cached, write, output int64) Record {
	return Record{ModelServed: model, TierServed: tier, Usage: &TokenUsage{
		InputTokens: pricingInt(input), CachedInputTokens: pricingInt(cached),
		CacheWriteTokens: pricingInt(write), OutputTokens: pricingInt(output),
	}}
}

func assertPricingUSD(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-10 {
		t.Fatalf("USD = %v, want %.10f", got, want)
	}
}

func TestOfficialRatesAndContextBoundary(t *testing.T) {
	cards := defaultRateCards()
	for _, test := range []struct {
		model                              string
		input, cached, write, output, fast float64
	}{
		{"gpt-6-astra", 10, 1, 12.5, 50, 2},
		{"gpt-6-sol", 2, .2, 2.5, 10, 2},
		{"gpt-6-luna", .1, .01, .125, .5, 2},
		{"gpt-5.6-sol", 4, .4, 5, 20, 2},
		{"gpt-5.6-terra", 2, .2, 2.5, 12, 2},
		{"gpt-5.6-luna", .2, .02, .25, 1.2, 2},
		{"gpt-5.5", 5, .5, 0, 30, 2.5},
		{"gpt-5.4", 2.5, .25, 0, 15, 2},
	} {
		t.Run(test.model, func(t *testing.T) {
			standard := findRateCard(cards, test.model, "standard")
			fast := findRateCard(cards, test.model, "fast")
			if standard == nil || fast == nil {
				t.Fatal("missing official card")
			}
			if standard.InputUSDPerMillion != test.input || standard.CachedInputUSDPerMillion != test.cached || standard.OutputUSDPerMillion != test.output {
				t.Fatal("incorrect Standard card", standard)
			}
			if fast.InputUSDPerMillion != test.input*test.fast || fast.OutputUSDPerMillion != test.output*test.fast {
				t.Fatal("incorrect Fast card", fast)
			}
			if test.write > 0 && (standard.CacheWriteUSDPerMillion == nil || *standard.CacheWriteUSDPerMillion != test.write) {
				t.Fatal("incorrect cache write")
			}
			short := calculateAPICost(pricingRecord(test.model, "default", 272000, 0, 0, 1000), cards)
			long := calculateAPICost(pricingRecord(test.model, "default", 272001, 0, 0, 1000), cards)
			assertPricingUSD(t, short.USD, .272*test.input+.001*test.output)
			assertPricingUSD(t, long.USD, .272001*test.input*2+.001*test.output*1.5)
		})
	}
}

func TestAPICacheWritesAreReplacementCategory(t *testing.T) {
	record := pricingRecord("gpt-6-sol", "priority", 10000, 4000, 3000, 2000)
	record.Usage.ReasoningTokens = pricingInt(1500)
	cost := calculateAPICost(record, defaultRateCards())
	assertPricingUSD(t, cost.InputUSD, .012)
	assertPricingUSD(t, cost.CachedInputUSD, .0016)
	assertPricingUSD(t, cost.CacheWriteUSD, .015)
	assertPricingUSD(t, cost.OutputUSD, .04)
	assertPricingUSD(t, cost.USD, .0686)
	if cost.RateCard.APITier != "fast" || cost.StandardRateCard.APITier != "standard" || cost.RateVersion == "" {
		t.Fatal("missing frozen rate snapshots")
	}
	features, reason := apiFeatures(record, cost)
	if reason != "" {
		t.Fatal(reason)
	}
	want := [3]float64{.012, .0008, .02}
	for i := range want {
		if math.Abs(features[i]-want[i]) > 1e-10 {
			t.Fatalf("features = %v, want %v", features, want)
		}
	}
}

func TestPricingIncompleteUsagePreservesKnownCategories(t *testing.T) {
	record := pricingRecord("gpt-6-astra", "default", 10000, 4000, 0, 2000)
	record.Usage.CacheWriteTokens = nil
	cost := calculateAPICost(record, defaultRateCards())
	if cost.USD != nil || cost.InputUSD != nil || cost.UnavailableReason != "missing-cache-write-tokens" {
		t.Fatalf("invented unknown writes: %+v", cost)
	}
	assertPricingUSD(t, cost.CachedInputUSD, .004)
	assertPricingUSD(t, cost.OutputUSD, .1)
	if _, reason := apiFeatures(record, cost); reason != "" {
		t.Fatalf("Codex prior should not need cache writes: %s", reason)
	}
	record.Usage.CachedInputTokens = nil
	if _, reason := apiFeatures(record, calculateAPICost(record, defaultRateCards())); reason != "missing-cached-input-tokens" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestPricingUnknownTierModelAndLongFastRemainUnavailable(t *testing.T) {
	for _, test := range []struct {
		model, tier, reason string
		input               int64
	}{
		{"gpt-6-sol", "", "unknown-api-tier", 100},
		{"gpt-6-sol", "auto", "unknown-api-tier", 100},
		{"gpt-6-sol", "scale", "unpublished-model-or-tier-rate", 100},
		{"gpt-6-sol-2099-01-01", "standard", "unpublished-model-or-tier-rate", 100},
		{"unknown", "standard", "unpublished-model-or-tier-rate", 100},
		{"gpt-5.5", "fast", "unpublished-long-context-rate", 272001},
		{"gpt-5.4", "fast", "unpublished-long-context-rate", 272001},
	} {
		record := pricingRecord(test.model, test.tier, test.input, 0, 0, 10)
		cost := calculateAPICost(record, defaultRateCards())
		if cost.USD != nil || cost.UnavailableReason != test.reason {
			t.Fatalf("%s %s: %+v", test.model, test.tier, cost)
		}
	}
}

func TestPricingServedModelAndTierOverrideRequest(t *testing.T) {
	record := pricingRecord("gpt-6-luna", "default", 10000, 0, 0, 1000)
	record.ModelRequested = "gpt-6-astra"
	record.TierRequested = "priority"
	cost := calculateAPICost(record, defaultRateCards())
	assertPricingUSD(t, cost.USD, .0015)
	record.ModelServed, record.TierServed = "", ""
	cost = calculateAPICost(record, defaultRateCards())
	assertPricingUSD(t, cost.USD, .3)
}

func TestPricingInvalidUsageDoesNotFabricateCost(t *testing.T) {
	for _, modify := range []func(*TokenUsage){
		func(u *TokenUsage) { u.InputTokens = pricingInt(-1) },
		func(u *TokenUsage) { u.CachedInputTokens = pricingInt(11) },
		func(u *TokenUsage) { u.CacheWriteTokens = pricingInt(9) },
		func(u *TokenUsage) { u.ReasoningTokens = pricingInt(11) },
	} {
		record := pricingRecord("gpt-6-sol", "standard", 10, 5, 0, 10)
		modify(record.Usage)
		cost := calculateAPICost(record, defaultRateCards())
		if cost.USD != nil || cost.UnavailableReason != "invalid-token-counts" {
			t.Fatalf("invalid usage cost: %+v", cost)
		}
	}
}

func TestSubscriptionFastPriorIsIndependentOfAPI(t *testing.T) {
	for _, test := range []struct {
		model string
		want  float64
	}{
		{"gpt-6-astra", 2.5}, {"gpt-6-sol", 2.5}, {"gpt-6-luna", 2.5},
		{"gpt-5.6-sol", 2.5}, {"gpt-5.5", 2.5}, {"gpt-5.4", 2},
	} {
		got, ok := subscriptionFastPrior(test.model)
		if !ok || got != test.want {
			t.Fatalf("%s: %v %v", test.model, got, ok)
		}
	}
	if _, ok := subscriptionFastPrior("unknown"); ok {
		t.Fatal("unknown model Fast prior")
	}
}
