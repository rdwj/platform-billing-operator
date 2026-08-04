package walkthroughs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// Handler serves the walkthrough BFF API endpoints.
type Handler struct {
	defs      map[string]*Definition
	client    ctrlclient.Client
	namespace string
}

// NewHandler creates a handler with the given walkthrough definitions,
// k8s client, and operator namespace.
func NewHandler(defs map[string]*Definition, client ctrlclient.Client, namespace string) *Handler {
	return &Handler{
		defs:      defs,
		client:    client,
		namespace: namespace,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/walkthroughs")
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "" && r.Method == http.MethodGet:
		h.handleList(w, r)
	case !strings.Contains(path, "/") && r.Method == http.MethodGet:
		h.handleGet(w, r, path)
	case strings.HasSuffix(path, "/status") && r.Method == http.MethodGet:
		name := strings.TrimSuffix(path, "/status")
		h.handleStatus(w, r, name)
	case strings.Contains(path, "/steps/") && strings.HasSuffix(path, "/execute") && r.Method == http.MethodPost:
		h.handleExecute(w, r, path)
	default:
		http.Error(w, "Not Found", http.StatusNotFound)
	}
}

func (h *Handler) handleList(w http.ResponseWriter, _ *http.Request) {
	result := make([]Metadata, 0, len(h.defs))
	for _, def := range h.defs {
		result = append(result, def.ToMetadata())
	}
	writeJSON(w, result)
}

func (h *Handler) handleGet(w http.ResponseWriter, _ *http.Request, name string) {
	def, ok := h.defs[name]
	if !ok {
		http.Error(w, fmt.Sprintf("walkthrough %q not found", name), http.StatusNotFound)
		return
	}
	writeJSON(w, def)
}

const (
	statusPending  = "pending"
	statusComplete = "complete"
	statusFailed   = "failed"
)

// StepStatus is the per-step verification result.
type StepStatus struct {
	StepID  string `json:"stepId"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request, name string) {
	def, ok := h.defs[name]
	if !ok {
		http.Error(w, fmt.Sprintf("walkthrough %q not found", name), http.StatusNotFound)
		return
	}

	statuses := make([]StepStatus, 0, len(def.Steps))
	for _, step := range def.Steps {
		statuses = append(statuses, h.evaluateStep(r.Context(), step))
	}
	writeJSON(w, statuses)
}

func (h *Handler) evaluateStep(ctx context.Context, step Step) StepStatus {
	if step.Verify == nil {
		return StepStatus{StepID: step.ID, Status: statusPending}
	}

	if step.Verify.Condition != nil {
		return h.evaluateCondition(ctx, step)
	}

	if step.Verify.BFFCall != "" {
		return StepStatus{
			StepID:  step.ID,
			Status:  statusPending,
			Message: "requires live environment probe",
		}
	}

	return StepStatus{StepID: step.ID, Status: statusPending}
}

func (h *Handler) evaluateCondition(ctx context.Context, step Step) StepStatus {
	probe := step.Verify.Condition

	kind := probe.Kind
	if kind == "" {
		kind = "BillingConnector"
	}
	name := probe.Name

	switch kind {
	case "BillingConnector":
		var bc billingv1alpha1.BillingConnector
		key := types.NamespacedName{Name: name, Namespace: h.namespace}
		if err := h.client.Get(ctx, key, &bc); err != nil {
			if apierrors.IsNotFound(err) {
				return StepStatus{
					StepID:  step.ID,
					Status:  statusPending,
					Message: fmt.Sprintf("BillingConnector %q not found", name),
				}
			}
			return StepStatus{
				StepID:  step.ID,
				Status:  statusFailed,
				Message: fmt.Sprintf("error reading BillingConnector: %v", err),
			}
		}

		for _, cond := range bc.Status.Conditions {
			if cond.Type == probe.Type {
				if cond.Status == metav1.ConditionTrue {
					return StepStatus{StepID: step.ID, Status: statusComplete}
				}

				ss := StepStatus{
					StepID:  step.ID,
					Status:  statusFailed,
					Reason:  cond.Reason,
					Message: cond.Message,
				}
				if hint, ok := step.Verify.FailureHints[cond.Reason]; ok {
					ss.Hint = hint
				}
				return ss
			}
		}

		return StepStatus{
			StepID:  step.ID,
			Status:  statusPending,
			Message: fmt.Sprintf("condition %q not yet set", probe.Type),
		}
	}

	return StepStatus{
		StepID:  step.ID,
		Status:  statusPending,
		Message: fmt.Sprintf("unsupported kind %q", kind),
	}
}

// ExecuteRequest is the JSON body for the step execute endpoint.
type ExecuteRequest struct {
	FormValues map[string]string `json:"formValues"`
}

// ExecuteResponse reports what resources were written.
type ExecuteResponse struct {
	Written []WrittenResource `json:"written"`
}

// WrittenResource describes a resource created or patched by execution.
type WrittenResource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func (h *Handler) handleExecute(w http.ResponseWriter, r *http.Request, path string) {
	// Parse: {name}/steps/{step}/execute
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[1] != "steps" || parts[3] != "execute" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	walkName, stepID := parts[0], parts[2]

	def, ok := h.defs[walkName]
	if !ok {
		http.Error(w, fmt.Sprintf("walkthrough %q not found", walkName), http.StatusNotFound)
		return
	}

	var step *Step
	for i := range def.Steps {
		if def.Steps[i].ID == stepID {
			step = &def.Steps[i]
			break
		}
	}
	if step == nil {
		http.Error(w, fmt.Sprintf("step %q not found in walkthrough %q", stepID, walkName), http.StatusNotFound)
		return
	}

	if len(step.Writes) == 0 {
		writeJSON(w, ExecuteResponse{Written: []WrittenResource{}})
		return
	}

	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	written, err := h.executeWrites(r.Context(), step.Writes, req.FormValues)
	if err != nil {
		http.Error(w, fmt.Sprintf("execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ExecuteResponse{Written: written})
}

func (h *Handler) executeWrites(
	ctx context.Context,
	writes []WriteSpec,
	formValues map[string]string,
) ([]WrittenResource, error) {
	data := map[string]any{
		"formValues": formValues,
		"namespace":  h.namespace,
	}

	var written []WrittenResource

	for _, ws := range writes {
		tmplStr := ws.Template
		if tmplStr == "" {
			tmplStr = ws.Patch
		}
		if tmplStr == "" {
			continue
		}

		tmpl, err := template.New("write").Option("missingkey=error").Parse(tmplStr)
		if err != nil {
			return nil, fmt.Errorf("parsing template for %s/%s: %w", ws.Kind, ws.Name, err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("executing template for %s/%s: %w", ws.Kind, ws.Name, err)
		}

		rendered := buf.Bytes()

		switch ws.Kind {
		case "Secret":
			if err := h.applySecret(ctx, ws.Name, rendered, ws.Patch != ""); err != nil {
				return nil, fmt.Errorf("applying Secret %s: %w", ws.Name, err)
			}
		case "BillingConnector":
			if err := h.applyBillingConnector(ctx, ws.Name, rendered, ws.Patch != ""); err != nil {
				return nil, fmt.Errorf("applying BillingConnector %s: %w", ws.Name, err)
			}
		default:
			return nil, fmt.Errorf("unsupported write kind %q", ws.Kind)
		}

		written = append(written, WrittenResource{Kind: ws.Kind, Name: ws.Name})
	}

	return written, nil
}

func (h *Handler) applySecret(ctx context.Context, name string, rendered []byte, isPatch bool) error {
	if isPatch {
		var patch struct {
			StringData map[string]string `json:"stringData" yaml:"stringData"`
		}
		if err := yaml.Unmarshal(rendered, &patch); err != nil {
			return fmt.Errorf("parsing patch: %w", err)
		}

		var existing corev1.Secret
		key := types.NamespacedName{Name: name, Namespace: h.namespace}
		if err := h.client.Get(ctx, key, &existing); err != nil {
			return fmt.Errorf("getting Secret for patch: %w", err)
		}

		if existing.StringData == nil {
			existing.StringData = make(map[string]string)
		}
		for k, v := range patch.StringData {
			existing.StringData[k] = v
		}
		return h.client.Update(ctx, &existing)
	}

	var secret corev1.Secret
	if err := yaml.Unmarshal(rendered, &secret); err != nil {
		return fmt.Errorf("parsing Secret YAML: %w", err)
	}
	secret.Namespace = h.namespace

	var existing corev1.Secret
	key := types.NamespacedName{Name: name, Namespace: h.namespace}
	if err := h.client.Get(ctx, key, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return h.client.Create(ctx, &secret)
		}
		return err
	}

	existing.StringData = secret.StringData
	return h.client.Update(ctx, &existing)
}

func (h *Handler) applyBillingConnector(ctx context.Context, name string, rendered []byte, isPatch bool) error {
	if isPatch {
		var patchData map[string]any
		if err := yaml.Unmarshal(rendered, &patchData); err != nil {
			return fmt.Errorf("parsing patch: %w", err)
		}

		patchBytes, err := json.Marshal(patchData)
		if err != nil {
			return fmt.Errorf("marshaling patch: %w", err)
		}

		var bc billingv1alpha1.BillingConnector
		key := types.NamespacedName{Name: name, Namespace: h.namespace}
		if err := h.client.Get(ctx, key, &bc); err != nil {
			return fmt.Errorf("getting BillingConnector for patch: %w", err)
		}

		return h.client.Patch(ctx, &bc, ctrlclient.RawPatch(types.MergePatchType, patchBytes))
	}

	var bc billingv1alpha1.BillingConnector
	if err := yaml.Unmarshal(rendered, &bc); err != nil {
		return fmt.Errorf("parsing BillingConnector YAML: %w", err)
	}
	bc.Namespace = h.namespace

	var existing billingv1alpha1.BillingConnector
	key := types.NamespacedName{Name: name, Namespace: h.namespace}
	if err := h.client.Get(ctx, key, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return h.client.Create(ctx, &bc)
		}
		return err
	}

	existing.Spec = bc.Spec
	return h.client.Update(ctx, &existing)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
