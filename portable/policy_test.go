package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuiltInPoliciesAreValid(t *testing.T) {
	for _, policy := range builtInPolicies {
		if err := validatePolicy(policy); err != nil {
			t.Fatalf("%s is invalid: %v", policy.ID, err)
		}
	}
}

func TestDecodePolicyRejectsUnknownAndUnsafeFields(t *testing.T) {
	policy := balancedPolicy()
	policy.ID = "custom"
	policy.BuiltIn = false
	document, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePolicy(document); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}

	unknown := strings.TrimSuffix(string(document), "}") + `,"script":"delete everything"}`
	if _, err := decodePolicy([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field was not rejected: %v", err)
	}
	policy.BuiltIn = true
	document, _ = json.Marshal(policy)
	if _, err := decodePolicy(document); err == nil {
		t.Fatal("imported policy declared as built-in")
	}
}

func TestPolicyThresholdsChangeAdviceWithoutWeakeningProtection(t *testing.T) {
	now := time.Now()
	cachePath := `/Users/me/Library/Caches/app/data.bin`
	modified := now.Add(-10 * 24 * time.Hour)
	conservative := adviseWithPolicy(builtInPolicies[0], cachePath, 100, modified, now)
	balanced := adviseWithPolicy(builtInPolicies[1], cachePath, 100, modified, now)
	reclaim := adviseWithPolicy(builtInPolicies[2], cachePath, 100, modified, now)
	if conservative.Level != "review" || balanced.Level != "safe" || reclaim.Level != "safe" {
		t.Fatalf("unexpected policy advice: %s, %s, %s", conservative.Level, balanced.Level, reclaim.Level)
	}
	for _, policy := range builtInPolicies {
		system := adviseWithPolicy(
			policy, `C:\Windows\System32\kernel.dll`, 100, modified, now,
		)
		if system.Level != "danger" {
			t.Fatalf("%s weakened system protection: %+v", policy.ID, system)
		}
	}
}

func TestValidatePolicyRejectsInvalidRanges(t *testing.T) {
	policy := balancedPolicy()
	policy.ID = "Bad ID"
	if err := validatePolicy(policy); err == nil {
		t.Fatal("invalid ID was accepted")
	}
	policy = balancedPolicy()
	policy.ID = "custom"
	policy.CacheHighConfidenceDays = policy.CacheMinAgeDays - 1
	if err := validatePolicy(policy); err == nil {
		t.Fatal("invalid cache thresholds were accepted")
	}
	policy = balancedPolicy()
	policy.ID = "custom"
	policy.LargeStaleMinBytes = 1
	if err := validatePolicy(policy); err == nil {
		t.Fatal("invalid size threshold was accepted")
	}
}
