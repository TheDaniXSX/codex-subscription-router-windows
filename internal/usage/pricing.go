package usage

import "strings"

// These rates were read on 2026-09-28 from
// https://developers.openai.com/api/docs/pricing and its pricing table data.
// The context boundary is <=272,000 input tokens for short context. The
// published Fast/priority API rates are independent of Codex credit factors.
const officialRateVersion = "openai-api-2026-09-28-v1"

func defaultRateCards() []RateCard {
	type price struct {
		model                        string
		input, cached, write, output float64
		fast                         float64
		fastLong                     bool
	}
	prices := []price{
		{"gpt-6-astra", 10, 1, 12.5, 50, 2, true},
		{"gpt-6-sol", 2, .2, 2.5, 10, 2, true},
		{"gpt-6-luna", .1, .01, .125, .5, 2, true},
		{"gpt-5.6-sol", 4, .4, 5, 20, 2, true},
		{"gpt-5.6-terra", 2, .2, 2.5, 12, 2, true},
		{"gpt-5.6-luna", .2, .02, .25, 1.2, 2, true},
		{"gpt-5.5", 5, .5, 0, 30, 2.5, false},
		{"gpt-5.4", 2.5, .25, 0, 15, 2, false},
	}
	var cards []RateCard
	for _, p := range prices {
		for _, tier := range []string{"standard", "fast"} {
			factor := 1.0
			if tier == "fast" {
				factor = p.fast
			}
			card := RateCard{
				Version: officialRateVersion, Model: p.model, APITier: tier,
				InputUSDPerMillion:       p.input * factor,
				CachedInputUSDPerMillion: p.cached * factor,
				OutputUSDPerMillion:      p.output * factor,
				ShortContextMaxTokens:    pricingInt(272000),
				LongContextThreshold:     pricingInt(272000),
			}
			if p.write > 0 {
				card.CacheWriteUSDPerMillion = pricingFloat(p.write * factor)
			}
			if tier == "standard" || p.fastLong {
				card.LongInputUSDPerMillion = pricingFloat(p.input * factor * 2)
				card.LongCachedUSDPerMillion = pricingFloat(p.cached * factor * 2)
				card.LongOutputUSDPerMillion = pricingFloat(p.output * factor * 1.5)
				if p.write > 0 {
					card.LongCacheWriteUSDPerMillion = pricingFloat(p.write * factor * 2)
				}
			}
			cards = append(cards, card)
		}
	}
	return cards
}

func pricingFloat(v float64) *float64 { return &v }
func pricingInt(v int64) *int64       { return &v }

func pricingModel(record Record) string {
	if model := strings.TrimSpace(record.ModelServed); model != "" {
		return model
	}
	return strings.TrimSpace(record.ModelRequested)
}

func pricingTier(tier string) string {
	switch strings.TrimSpace(tier) {
	case "default", "standard":
		return "standard"
	case "priority", "fast":
		return "fast"
	default:
		return strings.TrimSpace(tier)
	}
}

func findRateCard(cards []RateCard, model, tier string) *RateCard {
	for _, card := range cards {
		if card.Model == model && pricingTier(card.APITier) == tier {
			copy := card
			return &copy
		}
	}
	return nil
}

type categoryRates struct {
	input, cached, output float64
	write                 *float64
}

func ratesForContext(card RateCard, input *int64) (categoryRates, string) {
	rates := categoryRates{card.InputUSDPerMillion, card.CachedInputUSDPerMillion, card.OutputUSDPerMillion, card.CacheWriteUSDPerMillion}
	if input == nil {
		return categoryRates{}, "missing-input-tokens"
	}
	long := card.LongContextThreshold != nil && *input > *card.LongContextThreshold
	if card.ShortContextMaxTokens != nil && *input > *card.ShortContextMaxTokens {
		long = true
	}
	if long {
		if card.LongInputUSDPerMillion == nil || card.LongCachedUSDPerMillion == nil || card.LongOutputUSDPerMillion == nil {
			return categoryRates{}, "unpublished-long-context-rate"
		}
		rates = categoryRates{*card.LongInputUSDPerMillion, *card.LongCachedUSDPerMillion, *card.LongOutputUSDPerMillion, card.LongCacheWriteUSDPerMillion}
	}
	if !finite(rates.input) || !finite(rates.cached) || !finite(rates.output) || rates.input < 0 || rates.cached < 0 || rates.output < 0 ||
		(rates.write != nil && (!finite(*rates.write) || *rates.write < 0)) {
		return categoryRates{}, "invalid-rate-card"
	}
	return rates, ""
}

func validatePricingUsage(u *TokenUsage) string {
	if u == nil {
		return "missing-usage"
	}
	for _, count := range []*int64{u.InputTokens, u.CachedInputTokens, u.CacheWriteTokens, u.OutputTokens, u.ReasoningTokens, u.TotalTokens} {
		if count != nil && *count < 0 {
			return "invalid-token-counts"
		}
	}
	if u.InputTokens != nil {
		input := *u.InputTokens
		if u.CachedInputTokens != nil && *u.CachedInputTokens > input {
			return "invalid-token-counts"
		}
		if u.CacheWriteTokens != nil {
			if *u.CacheWriteTokens > input {
				return "invalid-token-counts"
			}
			if u.CachedInputTokens != nil && *u.CachedInputTokens > input-*u.CacheWriteTokens {
				return "invalid-token-counts"
			}
		}
	}
	if u.ReasoningTokens != nil && u.OutputTokens != nil && *u.ReasoningTokens > *u.OutputTokens {
		return "invalid-token-counts"
	}
	return ""
}

func calculateAPICost(record Record, cards []RateCard) APICost {
	model := pricingModel(record)
	result := APICost{StandardRateCard: findRateCard(cards, model, "standard")}
	tier := record.TierServed
	if strings.TrimSpace(tier) == "" {
		tier = record.TierRequested
	}
	tier = pricingTier(tier)
	if tier == "" || tier == "auto" {
		result.UnavailableReason = "unknown-api-tier"
		return result
	}
	card := findRateCard(cards, model, tier)
	if card == nil {
		result.UnavailableReason = "unpublished-model-or-tier-rate"
		return result
	}
	result.RateCard, result.RateVersion = card, card.Version
	if reason := validatePricingUsage(record.Usage); reason != "" {
		result.UnavailableReason = reason
		return result
	}
	u := record.Usage
	rates, reason := ratesForContext(*card, u.InputTokens)
	if reason != "" {
		result.UnavailableReason = reason
		return result
	}
	if u.OutputTokens != nil {
		result.OutputUSD = pricingFloat(float64(*u.OutputTokens) * rates.output / 1e6)
	}
	if u.CachedInputTokens != nil {
		result.CachedInputUSD = pricingFloat(float64(*u.CachedInputTokens) * rates.cached / 1e6)
	}
	var writes int64
	writeKnown := u.CacheWriteTokens != nil || card.CacheWriteUSDPerMillion == nil
	if u.CacheWriteTokens != nil {
		writes = *u.CacheWriteTokens
	}
	if writes > 0 && rates.write == nil {
		result.UnavailableReason = "unpublished-cache-write-rate"
		return result
	}
	if writeKnown {
		result.CacheWriteUSD = pricingFloat(0)
		if rates.write != nil {
			result.CacheWriteUSD = pricingFloat(float64(writes) * *rates.write / 1e6)
		}
	}
	if u.CachedInputTokens != nil && writeKnown {
		ordinary := *u.InputTokens - *u.CachedInputTokens - writes
		result.InputUSD = pricingFloat(float64(ordinary) * rates.input / 1e6)
	}
	switch {
	case !writeKnown:
		result.UnavailableReason = "missing-cache-write-tokens"
	case u.CachedInputTokens == nil:
		result.UnavailableReason = "missing-cached-input-tokens"
	case u.OutputTokens == nil:
		result.UnavailableReason = "missing-output-tokens"
	default:
		result.USD = pricingFloat(*result.InputUSD + *result.CachedInputUSD + *result.CacheWriteUSD + *result.OutputUSD)
	}
	return result
}

// apiFeatures uses the frozen Standard rate card as a relative quota prior.
// Codex credits have no separate cache-write charge, so all noncached input
// is valued at ordinary input rates. The caller applies the subscription Fast
// factor separately. See https://learn.chatgpt.com/docs/pricing.
func apiFeatures(record Record, cost APICost) ([3]float64, string) {
	var features [3]float64
	if reason := validatePricingUsage(record.Usage); reason != "" {
		return features, reason
	}
	card := cost.StandardRateCard
	if card == nil && cost.RateCard != nil && pricingTier(cost.RateCard.APITier) == "standard" {
		card = cost.RateCard
	}
	if card == nil {
		return features, "unpublished-standard-rate"
	}
	u := record.Usage
	rates, reason := ratesForContext(*card, u.InputTokens)
	if reason != "" {
		return features, reason
	}
	if u.CachedInputTokens == nil {
		return features, "missing-cached-input-tokens"
	}
	if u.OutputTokens == nil {
		return features, "missing-output-tokens"
	}
	features[0] = float64(*u.InputTokens-*u.CachedInputTokens) * rates.input / 1e6
	features[1] = float64(*u.CachedInputTokens) * rates.cached / 1e6
	features[2] = float64(*u.OutputTokens) * rates.output / 1e6
	return features, ""
}

// Published credit multipliers are initial hypotheses for included quota, not
// API prices. Source: https://learn.chatgpt.com/docs/agent-configuration/speed.
func subscriptionFastPrior(model string) (float64, bool) {
	switch model {
	case "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5":
		return 2.5, true
	case "gpt-5.4":
		return 2, true
	default:
		return 0, false
	}
}
