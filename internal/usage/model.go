package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// fitState is stored with its exact sample IDs and coefficients. Features are
// Standard API relative weights, not billed dollars. There is no intercept.
type fitState struct {
	Bucket           string    `json:"bucket"`
	Version          string    `json:"version"`
	Revision         int64     `json:"revision"`
	SampleIDs        []string  `json:"sampleIds"`
	Scale            float64   `json:"scale"`
	Mode             int       `json:"mode"`
	Keys             []string  `json:"keys,omitempty"`
	Coefficients     []float64 `json:"coefficients,omitempty"`
	BaseCoefficients []float64 `json:"baseCoefficients,omitempty"`
	ResidualMAD      float64   `json:"residualMad"`
	FastPrior        float64   `json:"fastPrior"`
}

type trainingPart struct {
	Model        string
	Fast         bool
	FastObserved bool
	Capacity     float64
	Features     [3]float64
}
type trainingSample struct {
	ID    string
	Y     float64
	Parts []trainingPart
}

func (f fitState) bucketStatus() BucketStatus {
	s := BucketStatus{Bucket: f.Bucket, SampleCount: len(f.SampleIDs), ModelVersion: f.Version, FeatureFit: f.Mode > 0, Confidence: "uncalibrated"}
	if len(f.SampleIDs) > 0 {
		s.Confidence = "provisional"
		// Retain the public field name; its units are relative USD per x20 pp.
		if f.Scale > 0 {
			s.ScalarUSDPerX20 = pricingFloat(1 / f.Scale)
		}
		s.ResidualMAD = pricingFloat(f.ResidualMAD)
	}
	return s
}

func (m *Manager) currentFit(ctx context.Context, bucket string) (fitState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fitState{}, err
	}
	defer tx.Rollback()
	f, err := currentFitTx(ctx, tx, bucket, m.options.FastPrior)
	if err != nil {
		return f, err
	}
	return f, tx.Commit()
}

func currentFitTx(ctx context.Context, tx *sql.Tx, bucket string, fastPrior float64) (fitState, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='calibration_revision'`).Scan(&revision); err != nil {
		return fitState{}, err
	}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT fit_json FROM model_versions WHERE bucket=? AND calibration_revision=?`, bucket, revision).Scan(&raw)
	if err == nil {
		var f fitState
		err = json.Unmarshal([]byte(raw), &f)
		return f, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fitState{}, err
	}
	views, err := loadObservationViews(ctx, tx, "")
	if err != nil {
		return fitState{}, err
	}
	var samples []trainingSample
	for _, v := range views {
		if v.Bucket != bucket || !v.Eligible || v.ObservedPercentX20 == nil {
			continue
		}
		s := trainingSample{ID: v.ID, Y: *v.ObservedPercentX20}
		valid := true
		for _, id := range v.RequestIDs {
			var recordRaw, costRaw string
			if err = tx.QueryRowContext(ctx, `SELECT record_json,api_cost_json FROM requests WHERE request_id=?`, id).Scan(&recordRaw, &costRaw); err != nil {
				return fitState{}, err
			}
			var r Record
			var c APICost
			if err = json.Unmarshal([]byte(recordRaw), &r); err != nil {
				return fitState{}, err
			}
			if err = json.Unmarshal([]byte(costRaw), &c); err != nil {
				return fitState{}, err
			}
			p, reason := quotaPart(r, c, v.CapacityMultiplier, fastPrior)
			if reason != "" {
				valid = false
				break
			}
			s.Parts = append(s.Parts, p)
		}
		if valid && len(s.Parts) > 0 {
			samples = append(samples, s)
		}
	}
	f := fitSamples(bucket, revision, fastPrior, samples)
	encoded, err := json.Marshal(f)
	if err != nil {
		return f, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO model_versions(bucket,calibration_revision,model_version,fit_json,created_ns) VALUES(?,?,?,?,?)`, bucket, revision, f.Version, string(encoded), time.Now().UnixNano())
	return f, err
}

func effectiveFast(r Record) (bool, string) {
	if r.Fast != nil {
		return *r.Fast, ""
	}
	switch pricingTier(first(r.TierServed, r.TierRequested)) {
	case "standard":
		return false, ""
	case "fast":
		return true, ""
	default:
		return false, "fast-mode-unknown"
	}
}

func quotaPart(r Record, c APICost, capacity, configuredPrior float64) (trainingPart, string) {
	p := trainingPart{Model: pricingModel(r), Capacity: capacity}
	x, reason := apiFeatures(r, c)
	if reason != "" {
		return p, reason
	}
	p.Fast, reason = effectiveFast(r)
	if reason != "" {
		return p, reason
	}
	p.FastObserved = pricingTier(r.TierServed) == "standard" || pricingTier(r.TierServed) == "fast"
	factor := 1.0
	if p.Fast {
		var known bool
		factor, known = subscriptionFastPrior(p.Model)
		if !known {
			return p, "subscription-fast-prior-unavailable"
		}
		// The documented model-specific exception (5.4 x2) is retained.
		if factor == 2.5 && configuredPrior > 0 {
			factor = configuredPrior
		}
	}
	for i := range x {
		p.Features[i] = x[i] * factor
	}
	return p, ""
}

func planCapacity(plan string) float64 {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "plus", "plus-x1":
		return 1
	case "prolite", "pro5", "pro-x5":
		return 5
	case "pro", "pro20", "pro-x20":
		return 20
	default:
		return 0
	}
}

func partKey(p trainingPart, mode int) string {
	switch mode {
	case 2:
		return p.Model
	case 3:
		return fmt.Sprintf("%s/fast=%t", p.Model, p.Fast)
	case 4:
		return fmt.Sprintf("%s/fast=%t/capacity=%g", p.Model, p.Fast, p.Capacity)
	default:
		return "all"
	}
}

func fitSamples(bucket string, revision int64, fastPrior float64, samples []trainingSample) fitState {
	f := fitState{Bucket: bucket, Revision: revision, FastPrior: fastPrior, Version: fmt.Sprintf("%s:%s:%d", EstimatorVersion, bucket, revision), SampleIDs: []string{}}
	var usable []trainingSample
	var ratios []float64
	for _, s := range samples {
		x := 0.0
		for _, p := range s.Parts {
			for _, v := range p.Features {
				x += v
			}
		}
		if x <= 0 || s.Y <= 0 || !finite(x) || !finite(s.Y) {
			continue
		}
		usable = append(usable, s)
		ratios = append(ratios, s.Y/x)
		f.SampleIDs = append(f.SampleIDs, s.ID)
	}
	if len(usable) == 0 {
		return f
	}
	// A median ratio initializes a robust nonnegative scale, including the
	// one-observation case. Repeated collinear data never enables extra terms.
	f.Scale = median(ratios)
	for mode := 1; mode <= 4; mode++ {
		if !modeSupported(usable, mode) {
			continue
		}
		keysSet := map[string]bool{}
		for _, s := range usable {
			for _, p := range s.Parts {
				keysSet[partKey(p, mode)] = true
			}
		}
		keys := make([]string, 0, len(keysSet))
		for k := range keysSet {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		dim := 3 * len(keys)
		if dim > 48 || len(usable) < 3*dim {
			continue
		}
		matrix := make([][]float64, len(usable))
		y := make([]float64, len(usable))
		for i, s := range usable {
			matrix[i] = make([]float64, dim)
			y[i] = s.Y
			for _, p := range s.Parts {
				index := sort.SearchStrings(keys, partKey(p, mode))
				for j, v := range p.Features {
					matrix[i][3*index+j] += v
				}
			}
		}
		if !identifiable(matrix) {
			continue
		}
		f.Mode, f.Keys, f.Coefficients = mode, keys, robustNNLS(matrix, y, f.Scale)
		if mode == 1 {
			f.BaseCoefficients = append([]float64(nil), f.Coefficients...)
		}
	}
	residuals := make([]float64, len(usable))
	for i, s := range usable {
		prediction := 0.0
		for _, p := range s.Parts {
			prediction += f.predictPart(p)
		}
		residuals[i] = math.Abs(s.Y - prediction)
	}
	f.ResidualMAD = median(residuals)
	return f
}

func (f fitState) partCoefficients(p trainingPart) []float64 {
	coeff := []float64{f.Scale, f.Scale, f.Scale}
	if len(f.BaseCoefficients) == 3 {
		copy(coeff, f.BaseCoefficients)
	}
	if f.Mode > 0 {
		index := sort.SearchStrings(f.Keys, partKey(p, f.Mode))
		if index < len(f.Keys) && f.Keys[index] == partKey(p, f.Mode) && 3*index+3 <= len(f.Coefficients) {
			copy(coeff, f.Coefficients[3*index:3*index+3])
		}
	}
	return coeff
}

func modeSupported(samples []trainingSample, mode int) bool {
	models := map[string]bool{}
	modes := map[string]map[bool]bool{}
	plans := map[string]map[float64]bool{}
	allFastObserved := true
	for _, s := range samples {
		for _, p := range s.Parts {
			models[p.Model] = true
			if modes[p.Model] == nil {
				modes[p.Model] = map[bool]bool{}
			}
			modes[p.Model][p.Fast] = true
			key := partKey(p, 3)
			if plans[key] == nil {
				plans[key] = map[float64]bool{}
			}
			plans[key][p.Capacity] = true
			allFastObserved = allFastObserved && p.FastObserved
		}
	}
	switch mode {
	case 2:
		return len(models) > 1
	case 3:
		if !allFastObserved {
			return false
		}
		for _, seen := range modes {
			if len(seen) < 2 {
				return false
			}
		}
	case 4:
		if !allFastObserved {
			return false
		}
		for _, seen := range plans {
			if len(seen) < 2 || seen[0] {
				return false
			}
		}
	}
	return true
}
func (f fitState) predictPart(p trainingPart) float64 {
	coeff := f.partCoefficients(p)
	total := 0.0
	for i, v := range p.Features {
		total += v * coeff[i]
	}
	return total
}

func freezePredictions(ctx context.Context, tx *sql.Tx, r Record, cost APICost, fastPrior float64) error {
	for _, bucket := range []string{BucketShort, BucketWeekly} {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM frozen_estimates WHERE request_id=? AND bucket=?`, r.RequestID, bucket).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		f, err := currentFitTx(ctx, tx, bucket, fastPrior)
		if err != nil {
			return err
		}
		e := Estimate{ModelVersion: f.Version, SampleCount: len(f.SampleIDs), Confidence: "uncalibrated", UnavailableReason: "pending-calibration"}
		p, reason := quotaPart(r, cost, planCapacity(r.AccountPlan), fastPrior)
		if reason != "" {
			e.UnavailableReason = reason
		} else if len(f.SampleIDs) > 0 {
			value := f.predictPart(p)
			if finite(value) && value >= 0 {
				e.PercentX20 = &value
				e.UnavailableReason = ""
				e.Confidence = "provisional"
				e.Coefficient = pricingFloat(f.Scale)
				e.FeatureCoefficients = f.partCoefficients(p)
				factor := 1.0
				if p.Fast {
					factor, _ = subscriptionFastPrior(p.Model)
					if factor == 2.5 {
						factor = fastPrior
					}
				}
				e.FastMultiplier = &factor
				if p.Capacity > 0 {
					e.PlanMultiplier = pricingFloat(p.Capacity)
				}
			}
		}
		encoded, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO frozen_estimates(request_id,bucket,estimate_json) VALUES(?,?,?)`, r.RequestID, bucket, string(encoded)); err != nil {
			return err
		}
	}
	return nil
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copied := append([]float64(nil), values...)
	sort.Float64s(copied)
	middle := len(copied) / 2
	if len(copied)%2 == 1 {
		return copied[middle]
	}
	return (copied[middle-1] + copied[middle]) / 2
}

// Test numerical rank after column normalization. Regularization alone must
// never be mistaken for evidence that collinear coefficients are identifiable.
func identifiable(x [][]float64) bool {
	if len(x) == 0 || len(x[0]) == 0 {
		return false
	}
	d := len(x[0])
	basis := make([][]float64, 0, d)
	for j := 0; j < d; j++ {
		v := make([]float64, len(x))
		norm := 0.0
		for i := range x {
			v[i] = x[i][j]
			norm += v[i] * v[i]
		}
		if norm <= 0 {
			return false
		}
		norm = math.Sqrt(norm)
		for i := range v {
			v[i] /= norm
		}
		for _, b := range basis {
			dot := 0.0
			for i := range v {
				dot += v[i] * b[i]
			}
			for i := range v {
				v[i] -= dot * b[i]
			}
		}
		remaining := 0.0
		for _, a := range v {
			remaining += a * a
		}
		if remaining < 1e-6 {
			return false
		}
		remaining = math.Sqrt(remaining)
		for i := range v {
			v[i] /= remaining
		}
		basis = append(basis, v)
	}
	return true
}

// Huber IRLS with nonnegative coordinate descent, weakly regularized toward
// the scalar prior. Normalize columns so token scale does not set the penalty.
func robustNNLS(x [][]float64, y []float64, prior float64) []float64 {
	n, d := len(x), len(x[0])
	scale := make([]float64, d)
	z := make([][]float64, n)
	for j := range scale {
		for i := range x {
			scale[j] += x[i][j] * x[i][j]
		}
		scale[j] = math.Sqrt(scale[j])
	}
	for i := range x {
		z[i] = make([]float64, d)
		for j := range scale {
			z[i][j] = x[i][j] / scale[j]
		}
	}
	beta, target := make([]float64, d), make([]float64, d)
	for j := range beta {
		target[j] = prior * scale[j]
		beta[j] = target[j]
	}
	weights := make([]float64, n)
	for i := range weights {
		weights[i] = 1
	}
	const lambda = 1e-6
	for outer := 0; outer < 8; outer++ {
		for iter := 0; iter < 500; iter++ {
			change := 0.0
			for j := 0; j < d; j++ {
				num, den := lambda*target[j], lambda
				for i := range z {
					residual := y[i]
					for k, b := range beta {
						if k != j {
							residual -= z[i][k] * b
						}
					}
					num += weights[i] * z[i][j] * residual
					den += weights[i] * z[i][j] * z[i][j]
				}
				value := math.Max(0, num/den)
				change = math.Max(change, math.Abs(value-beta[j]))
				beta[j] = value
			}
			if change < 1e-10 {
				break
			}
		}
		residuals := make([]float64, n)
		for i := range z {
			prediction := 0.0
			for j, b := range beta {
				prediction += z[i][j] * b
			}
			residuals[i] = math.Abs(y[i] - prediction)
		}
		mad := median(residuals)
		threshold := math.Max(1e-9, 1.345*1.4826*mad)
		for i, r := range residuals {
			weights[i] = 1
			if r > threshold {
				weights[i] = threshold / r
			}
		}
	}
	for j := range beta {
		beta[j] /= scale[j]
	}
	return beta
}
