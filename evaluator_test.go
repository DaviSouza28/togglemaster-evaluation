package main

import (
"encoding/json"
"net/http"
"net/http/httptest"
"strings"
"testing"
)

func TestHealthHandler(t *testing.T) {
app := &App{}

req := httptest.NewRequest(http.MethodGet, "/health", nil)
rec := httptest.NewRecorder()

app.healthHandler(rec, req)

if rec.Code != http.StatusOK {
t.Fatalf("expected status 200, got %d", rec.Code)
}

var body map[string]string
if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
t.Fatalf("failed to decode response: %v", err)
}

if body["status"] != "ok" {
t.Fatalf("expected status ok, got %q", body["status"])
}
}

func TestBuildServiceURL(t *testing.T) {
got, err := buildServiceURL(
"http://flag-service:8002",
"flags",
"checkout-v2",
)

if err != nil {
t.Fatalf("unexpected error: %v", err)
}

want := "http://flag-service:8002/flags/checkout-v2"

if got != want {
t.Fatalf("expected %q, got %q", want, got)
}
}

func TestBuildServiceURLRejectsInvalidScheme(t *testing.T) {
_, err := buildServiceURL(
"file:///etc/passwd",
"flags",
"checkout-v2",
)

if err == nil {
t.Fatal("expected invalid scheme error")
}
}

func TestBuildServiceURLDoesNotAllowFlagToChangeHost(t *testing.T) {
got, err := buildServiceURL(
"http://flag-service:8002",
"flags",
"//evil.example.com/test",
)

if err != nil {
t.Fatalf("unexpected error: %v", err)
}

if !strings.HasPrefix(got, "http://flag-service:8002/") {
t.Fatalf("URL escaped trusted host: %q", got)
}
}

func TestDeterministicBucketIsStable(t *testing.T) {
input := "user-123checkout-v2"

first := getDeterministicBucket(input)
second := getDeterministicBucket(input)

if first != second {
t.Fatalf("bucket is not deterministic: %d != %d", first, second)
}

if first < 0 || first > 99 {
t.Fatalf("bucket outside expected range: %d", first)
}
}

func TestEvaluationReturnsFalseWhenFlagDisabled(t *testing.T) {
app := &App{}

info := &CombinedFlagInfo{
Flag: &Flag{
Name:      "checkout-v2",
IsEnabled: false,
},
}

result := app.runEvaluationLogic(info, "user-123")

if result {
t.Fatal("expected disabled flag to evaluate to false")
}
}

func TestEvaluationReturnsTrueWithoutTargetingRule(t *testing.T) {
app := &App{}

info := &CombinedFlagInfo{
Flag: &Flag{
Name:      "checkout-v2",
IsEnabled: true,
},
Rule: nil,
}

result := app.runEvaluationLogic(info, "user-123")

if !result {
t.Fatal("expected enabled flag without targeting rule to evaluate to true")
}
}
