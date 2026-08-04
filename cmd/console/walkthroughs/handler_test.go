package walkthroughs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "billing-system"

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	billingv1alpha1.AddToScheme(s) //nolint:errcheck
	corev1.AddToScheme(s)          //nolint:errcheck
	return s
}

func testDefs() map[string]*Definition {
	return map[string]*Definition{
		"stripe": {
			ID:    "stripe",
			Title: "Connect Stripe",
			Steps: []Step{
				{
					ID:    "credentials",
					Title: "API credentials",
					Form:  []FormField{{Name: "apiKey", Kind: "secret"}},
					Writes: []WriteSpec{
						{
							Kind: "Secret",
							Name: "stripe-keys",
							Template: `apiVersion: v1
kind: Secret
metadata:
  name: stripe-keys
type: Opaque
stringData:
  secretKey: "{{.formValues.apiKey}}"
`,
						},
						{
							Kind: "BillingConnector",
							Name: "stripe",
							Template: `apiVersion: billing.opendatahub.io/v1alpha1
kind: BillingConnector
metadata:
  name: stripe
spec:
  type: stripe
  credentialsSecretRef:
    name: stripe-keys
  taxMode: none
`,
						},
					},
					Verify: &VerifySpec{
						Condition: &ConditionProbe{
							Kind: "BillingConnector",
							Name: "stripe",
							Type: "CredentialsValid",
						},
						FailureHints: map[string]string{
							"InvalidKey": "Check your API key",
						},
					},
				},
				{
					ID:    "enable",
					Title: "Enable sync",
					Writes: []WriteSpec{
						{
							Kind: "BillingConnector",
							Name: "stripe",
							Patch: `spec:
  sync:
    usageRecordsInterval: "5m"
`,
						},
					},
					Verify: &VerifySpec{
						Condition: &ConditionProbe{
							Type: "SyncHealthy",
							Name: "stripe",
						},
					},
				},
				{
					ID:    "dry-run",
					Title: "Test reload",
					Verify: &VerifySpec{
						BFFCall: "/api/connectors/stripe/dry-run-reload",
					},
				},
				{
					ID:    "no-verify",
					Title: "Info step",
					Text:  "Just informational",
				},
			},
		},
	}
}

func newTestHandler(objs ...ctrlclient.Object) *Handler {
	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(objs...).
		WithStatusSubresource(&billingv1alpha1.BillingConnector{}).
		Build()
	return NewHandler(testDefs(), c, testNamespace)
}

func TestHandleList(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest("GET", "/api/walkthroughs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var result []Metadata
	json.NewDecoder(rec.Body).Decode(&result) //nolint:errcheck
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].ID != "stripe" {
		t.Errorf("id = %q, want stripe", result[0].ID)
	}
	if result[0].StepCount != 4 {
		t.Errorf("stepCount = %d, want 4", result[0].StepCount)
	}
}

func TestHandleGet_Found(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest("GET", "/api/walkthroughs/stripe", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var def Definition
	json.NewDecoder(rec.Body).Decode(&def) //nolint:errcheck
	if def.ID != "stripe" {
		t.Errorf("id = %q, want stripe", def.ID)
	}
	if len(def.Steps) != 4 {
		t.Errorf("steps = %d, want 4", len(def.Steps))
	}
}

func TestHandleGet_NotFound(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest("GET", "/api/walkthroughs/nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleStatus_NoBillingConnector(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest("GET", "/api/walkthroughs/stripe/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var statuses []StepStatus
	json.NewDecoder(rec.Body).Decode(&statuses) //nolint:errcheck
	if len(statuses) != 4 {
		t.Fatalf("statuses = %d, want 4", len(statuses))
	}

	// Credentials step: BillingConnector doesn't exist → pending
	if statuses[0].Status != statusPending {
		t.Errorf("step 0 status = %q, want %q", statuses[0].Status, statusPending)
	}
	if statuses[0].Message == "" {
		t.Error("step 0 should have a message about missing BC")
	}
}

func TestHandleStatus_ConditionTrue(t *testing.T) {
	bc := &billingv1alpha1.BillingConnector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stripe",
			Namespace: testNamespace,
		},
		Spec: billingv1alpha1.BillingConnectorSpec{
			Type:                 "stripe",
			CredentialsSecretRef: corev1.LocalObjectReference{Name: "stripe-keys"},
		},
		Status: billingv1alpha1.BillingConnectorStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "CredentialsValid",
					Status: metav1.ConditionTrue,
					Reason: "Valid",
				},
			},
		},
	}

	h := newTestHandler(bc)

	req := httptest.NewRequest("GET", "/api/walkthroughs/stripe/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var statuses []StepStatus
	json.NewDecoder(rec.Body).Decode(&statuses) //nolint:errcheck

	if statuses[0].Status != statusComplete {
		t.Errorf("step 0 status = %q, want %q", statuses[0].Status, statusComplete)
	}
}

func TestHandleStatus_ConditionFalse_WithHint(t *testing.T) {
	bc := &billingv1alpha1.BillingConnector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stripe",
			Namespace: testNamespace,
		},
		Spec: billingv1alpha1.BillingConnectorSpec{
			Type:                 "stripe",
			CredentialsSecretRef: corev1.LocalObjectReference{Name: "stripe-keys"},
		},
		Status: billingv1alpha1.BillingConnectorStatus{
			Conditions: []metav1.Condition{
				{
					Type:    "CredentialsValid",
					Status:  metav1.ConditionFalse,
					Reason:  "InvalidKey",
					Message: "API key rejected",
				},
			},
		},
	}

	h := newTestHandler(bc)

	req := httptest.NewRequest("GET", "/api/walkthroughs/stripe/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var statuses []StepStatus
	json.NewDecoder(rec.Body).Decode(&statuses) //nolint:errcheck

	if statuses[0].Status != statusFailed {
		t.Errorf("step 0 status = %q, want %q", statuses[0].Status, statusFailed)
	}
	if statuses[0].Reason != "InvalidKey" {
		t.Errorf("step 0 reason = %q, want InvalidKey", statuses[0].Reason)
	}
	if statuses[0].Hint != "Check your API key" {
		t.Errorf("step 0 hint = %q, want 'Check your API key'", statuses[0].Hint)
	}
}

func TestHandleStatus_BFFCallStep(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest("GET", "/api/walkthroughs/stripe/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var statuses []StepStatus
	json.NewDecoder(rec.Body).Decode(&statuses) //nolint:errcheck

	// dry-run step (index 2) has a bffCall verify → pending with message
	if statuses[2].Status != statusPending {
		t.Errorf("bffCall step status = %q, want %q", statuses[2].Status, statusPending)
	}
	if statuses[2].Message == "" {
		t.Error("bffCall step should have a message about live env")
	}
}

func TestHandleStatus_NoVerifyStep(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest("GET", "/api/walkthroughs/stripe/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var statuses []StepStatus
	json.NewDecoder(rec.Body).Decode(&statuses) //nolint:errcheck

	// no-verify step (index 3) has no verify → pending
	if statuses[3].Status != statusPending {
		t.Errorf("no-verify step status = %q, want %q", statuses[3].Status, statusPending)
	}
}

func TestHandleExecute_CreatesSecretAndBillingConnector(t *testing.T) {
	h := newTestHandler()

	body := ExecuteRequest{
		FormValues: map[string]string{
			"apiKey": "rk_test_abc123",
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(
		"POST",
		"/api/walkthroughs/stripe/steps/credentials/execute",
		bytes.NewReader(bodyBytes),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var resp ExecuteResponse
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if len(resp.Written) != 2 {
		t.Fatalf("written = %d, want 2", len(resp.Written))
	}

	// Verify Secret was created with the form value
	var secret corev1.Secret
	err := h.client.Get(context.Background(), types.NamespacedName{
		Name: "stripe-keys", Namespace: testNamespace,
	}, &secret)
	if err != nil {
		t.Fatalf("getting Secret: %v", err)
	}
	if secret.StringData["secretKey"] != "rk_test_abc123" {
		t.Errorf("secret key = %q, want rk_test_abc123",
			secret.StringData["secretKey"])
	}

	// Verify BillingConnector was created
	var bc billingv1alpha1.BillingConnector
	err = h.client.Get(context.Background(), types.NamespacedName{
		Name: "stripe", Namespace: testNamespace,
	}, &bc)
	if err != nil {
		t.Fatalf("getting BillingConnector: %v", err)
	}
	if bc.Spec.Type != "stripe" {
		t.Errorf("bc.Spec.Type = %q, want stripe", bc.Spec.Type)
	}
	if bc.Spec.CredentialsSecretRef.Name != "stripe-keys" {
		t.Errorf("credentialsSecretRef = %q, want stripe-keys",
			bc.Spec.CredentialsSecretRef.Name)
	}
}

func TestHandleExecute_PatchesBillingConnector(t *testing.T) {
	bc := &billingv1alpha1.BillingConnector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stripe",
			Namespace: testNamespace,
		},
		Spec: billingv1alpha1.BillingConnectorSpec{
			Type:                 "stripe",
			CredentialsSecretRef: corev1.LocalObjectReference{Name: "stripe-keys"},
		},
	}

	h := newTestHandler(bc)

	body := ExecuteRequest{FormValues: map[string]string{}}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(
		"POST",
		"/api/walkthroughs/stripe/steps/enable/execute",
		bytes.NewReader(bodyBytes),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var updated billingv1alpha1.BillingConnector
	err := h.client.Get(context.Background(), types.NamespacedName{
		Name: "stripe", Namespace: testNamespace,
	}, &updated)
	if err != nil {
		t.Fatalf("getting patched BillingConnector: %v", err)
	}
	if updated.Spec.Sync == nil {
		t.Fatal("expected Sync to be set after patch")
	}
	if updated.Spec.Sync.UsageRecordsInterval != "5m" {
		t.Errorf("usageRecordsInterval = %q, want 5m",
			updated.Spec.Sync.UsageRecordsInterval)
	}
}

func TestHandleExecute_StepNotFound(t *testing.T) {
	h := newTestHandler()

	body := ExecuteRequest{FormValues: map[string]string{}}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(
		"POST",
		"/api/walkthroughs/stripe/steps/nonexistent/execute",
		bytes.NewReader(bodyBytes),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleExecute_WalkthroughNotFound(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest(
		"POST",
		"/api/walkthroughs/nonexistent/steps/s1/execute",
		nil,
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleExecute_NoWritesStep(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest(
		"POST",
		"/api/walkthroughs/stripe/steps/dry-run/execute",
		nil,
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp ExecuteResponse
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if len(resp.Written) != 0 {
		t.Errorf("written = %d, want 0", len(resp.Written))
	}
}

func TestServeHTTP_MethodNotAllowed(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest("DELETE", "/api/walkthroughs/stripe", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
