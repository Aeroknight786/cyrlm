package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/runtime"
)

func calibrationCheck(ctx context.Context, home, engine, path string, jsonOut bool) error {
	// Header matching is local only. Resolve it before hardware or engine work.
	m, q, err := calib.MatchFile(path)
	if err != nil {
		return err
	}
	c, err := calib.Load(home)
	if err != nil {
		return fmt.Errorf("load calibration before --check: %w", err)
	}
	fp, err := calib.MachineFingerprint(ctx, engine)
	if err != nil {
		return err
	}
	if fp.Hash != c.Fingerprint.Hash {
		return errors.New("calibration is stale; run trialm calibrate before --check")
	}
	var flags common
	target, _, err := flags.target()
	if err != nil {
		return err
	}
	profile, _ := runtime.Get("llamacpp")
	r := predict.WithCalibration(predict.Input{Model: m, QuantName: q.Name, Runtime: profile, Target: target, Tuned: true, CtxOverride: 65536, KVCacheType: "f16"}, c)
	if r.Kind != "calibrated" {
		return errors.New("this file has no usable calibrated GPU prediction; run trialm calibrate and inspect coverage")
	}
	if !jsonOut {
		fmt.Fprintf(os.Stderr, "Checking %s %s at depth 0 and 32k. About 1–2 min on the M5; larger models can take longer. No downloads.\n", m.ID, q.Name)
	}
	rows, err := calib.Check(ctx, calib.CheckOptions{Home: home, Engine: engine, Path: path, Model: m.ID, Quant: q.Name, Fingerprint: fp, Predict: calibrationCheckPredictor(r)})
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"v": 1, "type": "calibration_validation", "validations": rows})
	}
	for _, v := range rows {
		mode := "prefill"
		if v.Gen > 0 {
			mode = "decode"
		}
		result := "inside band"
		if !v.InsideBand {
			result = "MISSED band"
		}
		fmt.Printf("%s depth %d: measured %.2g tok/s, calibrated %.2g tok/s ±%d%%; error %+.0f%%; %s (%s)\n", mode, v.Depth, v.MeasuredTPS, v.PredictedTPS, v.BandPct, v.ErrorPct, result, v.LoadMode)
	}
	fmt.Println("Saved validation rows in calib.json. Synthetic fits are unchanged.")
	return nil
}

func calibrationCheckPredictor(r predict.Result) func(calib.BenchRow) (calib.CheckPrediction, error) {
	return func(b calib.BenchRow) (calib.CheckPrediction, error) {
		if r.Kind != "calibrated" || r.Timing == nil {
			return calib.CheckPrediction{}, errors.New("validation requires a calibrated prediction")
		}
		band := r.Accuracy.PrefillPct
		var seconds float64
		if b.Gen > 0 {
			band = r.Accuracy.DecodePct
			for i := 0; i < b.Gen; i++ {
				seconds += r.Timing.DecodeSec(b.Depth + i)
			}
		} else {
			seconds = r.Timing.PrefillSec(b.Depth, b.Depth+b.Prompt)
		}
		if seconds <= 0 {
			return calib.CheckPrediction{}, errors.New("invalid validation token count")
		}
		return calib.CheckPrediction{TPS: float64(b.Prompt+b.Gen) / seconds, BandPct: band, Basis: r.Accuracy.Basis}, nil
	}
}
