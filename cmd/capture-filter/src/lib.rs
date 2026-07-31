use log::{debug, error, info, warn};
use proxy_wasm::traits::{Context, HttpContext, RootContext};
use proxy_wasm::types::{Action, ContextType, LogLevel};
use serde::Deserialize;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

const COLLECTOR_VERSION: &str = "0.1.0";

proxy_wasm::main! {{
    proxy_wasm::set_log_level(LogLevel::Info);
    proxy_wasm::set_root_context(|_| -> Box<dyn RootContext> {
        Box::new(CaptureRoot {
            ledger_cluster: default_ledger_cluster(),
            authz_filter_name: default_authz_filter(),
        })
    });
}}

// ---------------------------------------------------------------------------
// Data types
// ---------------------------------------------------------------------------

/// Token usage extracted from an OpenAI-compatible response.
#[derive(Debug, Clone, Deserialize)]
struct UsageData {
    prompt_tokens: u64,
    completion_tokens: u64,
    total_tokens: u64,
    #[serde(default)]
    completion_tokens_details: Option<CompletionTokensDetails>,
    #[serde(default)]
    prompt_tokens_details: Option<PromptTokensDetails>,
}

#[derive(Debug, Clone, Deserialize)]
struct CompletionTokensDetails {
    #[serde(default)]
    reasoning_tokens: u64,
}

#[derive(Debug, Clone, Deserialize)]
struct PromptTokensDetails {
    #[serde(default)]
    cached_tokens: u64,
}

impl UsageData {
    fn reasoning_tokens(&self) -> u64 {
        self.completion_tokens_details
            .as_ref()
            .map(|d| d.reasoning_tokens)
            .unwrap_or(0)
    }

    fn cached_tokens(&self) -> u64 {
        self.prompt_tokens_details
            .as_ref()
            .map(|d| d.cached_tokens)
            .unwrap_or(0)
    }
}

/// Plugin configuration parsed from the WasmPlugin/EnvoyFilter config JSON.
#[derive(Debug, Deserialize)]
struct FilterConfig {
    #[serde(default = "default_ledger_cluster")]
    ledger_cluster: String,
    /// Authorino filter name in Envoy's dynamic metadata. Override this if the
    /// deployment uses a custom ext_authz filter name.
    #[serde(default = "default_authz_filter")]
    authz_filter_name: String,
}

fn default_ledger_cluster() -> String {
    "ledger-svc".to_string()
}

fn default_authz_filter() -> String {
    "envoy.filters.http.ext_authz".to_string()
}

// ---------------------------------------------------------------------------
// Root context -- created once per worker thread.
// Reads plugin configuration and spawns per-request CaptureFilter contexts.
// ---------------------------------------------------------------------------

struct CaptureRoot {
    ledger_cluster: String,
    authz_filter_name: String,
}

impl Context for CaptureRoot {}

impl RootContext for CaptureRoot {
    fn on_vm_start(&mut self, _vm_configuration_size: usize) -> bool {
        info!("capture-filter loaded");
        // The WasmPlugin/EnvoyFilter MUST configure fail_open=true so that
        // gateway serving continues if this filter crashes. Metering gaps
        // are acceptable; blocking inference is not.
        true
    }

    fn on_configure(&mut self, _plugin_configuration_size: usize) -> bool {
        match self.get_plugin_configuration() {
            Ok(Some(config_bytes)) => {
                match serde_json::from_slice::<FilterConfig>(&config_bytes) {
                    Ok(config) => {
                        info!(
                            "capture-filter configured: ledger_cluster={}, authz_filter={}",
                            config.ledger_cluster, config.authz_filter_name
                        );
                        self.ledger_cluster = config.ledger_cluster;
                        self.authz_filter_name = config.authz_filter_name;
                    }
                    Err(e) => {
                        warn!(
                            "capture-filter: failed to parse config, using defaults: {}",
                            e
                        );
                    }
                }
            }
            Ok(None) => {
                info!("capture-filter: no plugin config, using defaults");
            }
            Err(e) => {
                warn!("capture-filter: failed to read config: {:?}", e);
            }
        }
        true
    }

    fn get_type(&self) -> Option<ContextType> {
        Some(ContextType::HttpContext)
    }

    fn create_http_context(&self, context_id: u32) -> Option<Box<dyn HttpContext>> {
        Some(Box::new(CaptureFilter {
            context_id,
            ledger_cluster: self.ledger_cluster.clone(),
            authz_filter_name: self.authz_filter_name.clone(),
            is_streaming: false,
            model: None,
            upstream_request_id: None,
            upstream_status_code: None,
            tenant_id: None,
            key_id: None,
            sse_buffer: String::new(),
            captured_usage: None,
            usage_emitted: false,
            dispatch_pending: false,
        }))
    }
}

// ---------------------------------------------------------------------------
// Per-request context -- one instance per HTTP stream.
// Handles include_usage injection (request) and SSE/JSON response parsing.
// ---------------------------------------------------------------------------

struct CaptureFilter {
    context_id: u32,
    ledger_cluster: String,
    /// Authorino filter name in Envoy's dynamic metadata tree.
    authz_filter_name: String,
    is_streaming: bool,
    model: Option<String>,
    upstream_request_id: Option<String>,
    /// HTTP status code from the upstream response headers.
    upstream_status_code: Option<u32>,
    /// Billing tenant identity, extracted from Authorino filter metadata.
    tenant_id: Option<String>,
    /// API key identifier (not the secret), extracted from Authorino filter
    /// metadata.
    key_id: Option<String>,
    /// Accumulator for SSE chunks that may span callback boundaries.
    sse_buffer: String,
    /// Usage data extracted from the response (streaming or non-streaming).
    captured_usage: Option<UsageData>,
    /// Guard to prevent duplicate emission.
    usage_emitted: bool,
    /// True after dispatch_http_call — pause the response until
    /// on_http_call_response fires so the context stays alive.
    dispatch_pending: bool,
}

impl Context for CaptureFilter {
    fn on_http_call_response(
        &mut self,
        _token_id: u32,
        _num_headers: usize,
        body_size: usize,
        _num_trailers: usize,
    ) {
        self.dispatch_pending = false;
        self.resume_http_response();

        if let Ok(Some(status)) = self.get_http_call_response_header(":status") {
            if !status.starts_with('2') {
                let body_msg = self
                    .get_http_call_response_body(0, body_size)
                    .ok()
                    .flatten()
                    .map(|b| String::from_utf8_lossy(&b).into_owned())
                    .unwrap_or_default();
                warn!(
                    "capture-filter[{}]: ledger-svc returned {}: {}",
                    self.context_id, status, body_msg
                );
            }
        }
    }
}

impl HttpContext for CaptureFilter {
    // ---- Request path -------------------------------------------------------

    fn on_http_request_headers(&mut self, _num_headers: usize, _end_of_stream: bool) -> Action {
        // Grab x-request-id early (Envoy typically generates one).
        if let Ok(Some(req_id)) = self.get_http_request_header("x-request-id") {
            self.upstream_request_id = Some(req_id);
        }

        // Read identity from Authorino filter metadata (set by ext_authz earlier
        // in the chain). The metadata path depends on how Authorino is configured;
        // the filter name is configurable via `authz_filter_name`.
        self.tenant_id = self.get_authorino_metadata("tenant_id");
        self.key_id = self.get_authorino_metadata("key_id");

        if self.tenant_id.is_none() {
            warn!(
                "capture-filter[{}]: tenant_id not found in Authorino metadata, \
                 will use 'unknown'",
                self.context_id
            );
        }

        // Remove Content-Length so we can safely modify the body in
        // on_http_request_body (inject include_usage). With HTTP/2, Envoy
        // uses DATA frame lengths for framing; a stale Content-Length
        // from the original request causes a 400 after body modification.
        let _ = self.set_http_request_header("content-length", None);

        Action::Continue
    }

    fn on_http_request_body(&mut self, body_size: usize, end_of_stream: bool) -> Action {
        // Buffer until we have the complete request body.
        if !end_of_stream {
            return Action::Pause;
        }

        let body = match self.get_http_request_body(0, body_size) {
            Ok(Some(b)) => b,
            _ => return Action::Continue,
        };

        // Try to parse as JSON. Non-JSON request bodies pass through unchanged.
        let mut json: serde_json::Value = match serde_json::from_slice(&body) {
            Ok(v) => v,
            Err(_) => return Action::Continue,
        };

        // Extract model name for the usage event's `sku` field.
        if let Some(model) = json.get("model").and_then(|v| v.as_str()) {
            self.model = Some(model.to_string());
        }

        // Determine streaming vs non-streaming.
        let is_streaming = json
            .get("stream")
            .and_then(|v| v.as_bool())
            .unwrap_or(false);
        self.is_streaming = is_streaming;

        if is_streaming {
            // Inject `stream_options.include_usage = true` so the upstream LLM
            // emits a usage-bearing chunk at the end of the SSE stream.
            if let Some(obj) = json.as_object_mut() {
                let stream_opts = obj
                    .entry("stream_options")
                    .or_insert_with(|| serde_json::json!({}));
                if let Some(opts) = stream_opts.as_object_mut() {
                    opts.insert("include_usage".to_string(), serde_json::json!(true));
                }
            }

            let new_body = match serde_json::to_vec(&json) {
                Ok(b) => b,
                Err(_) => return Action::Continue,
            };
            let _ = self.set_http_request_body(0, body_size, &new_body);
            let _ = self.set_http_request_header(
                "content-length",
                Some(&new_body.len().to_string()),
            );
            debug!(
                "capture-filter[{}]: injected include_usage, model={}",
                self.context_id,
                self.model.as_deref().unwrap_or("?")
            );
        } else {
            debug!(
                "capture-filter[{}]: non-streaming request, model={}",
                self.context_id,
                self.model.as_deref().unwrap_or("?")
            );
        }

        Action::Continue
    }

    // ---- Response path ------------------------------------------------------

    fn on_http_response_headers(&mut self, _num_headers: usize, _end_of_stream: bool) -> Action {
        // Pick up x-request-id from the response if we missed it on the request.
        if self.upstream_request_id.is_none() {
            if let Ok(Some(req_id)) = self.get_http_response_header("x-request-id") {
                self.upstream_request_id = Some(req_id);
            }
        }

        // Track upstream status for failure detection in on_log / end_of_stream.
        if let Ok(Some(status)) = self.get_http_response_header(":status") {
            if let Ok(code) = status.parse::<u32>() {
                self.upstream_status_code = Some(code);
                if code >= 500 {
                    warn!(
                        "capture-filter[{}]: upstream returned {}",
                        self.context_id, code
                    );
                }
            }
        }

        Action::Continue
    }

    fn on_http_response_body(&mut self, body_size: usize, end_of_stream: bool) -> Action {
        if self.is_streaming {
            self.handle_streaming_body(body_size, end_of_stream)
        } else {
            self.handle_non_streaming_body(body_size, end_of_stream)
        }
    }

    fn on_log(&mut self) {
        // Safety net: emit captured usage if [DONE] was never received (e.g.
        // client disconnect, upstream timeout). Detect the failure mode and
        // set the status accordingly.
        if !self.usage_emitted {
            let status = self.detect_failure_status();
            if let Some(usage) = self.captured_usage.clone() {
                warn!(
                    "capture-filter[{}]: emitting usage in on_log (status={})",
                    self.context_id, status
                );
                self.emit_usage_event(&usage, &status, true);
            } else if status != "ok" {
                // No usage captured at all -- emit a minimal event for audit trail.
                warn!(
                    "capture-filter[{}]: no usage captured, emitting zero-usage \
                     audit event (status={})",
                    self.context_id, status
                );
                let empty_usage = UsageData {
                    prompt_tokens: 0,
                    completion_tokens: 0,
                    total_tokens: 0,
                    completion_tokens_details: None,
                    prompt_tokens_details: None,
                };
                self.emit_usage_event(&empty_usage, &status, true);
            }
        }
    }
}

// ---------------------------------------------------------------------------
// CaptureFilter -- helper methods
// ---------------------------------------------------------------------------

impl CaptureFilter {
    /// Read a single key from Authorino's dynamic metadata.
    ///
    /// Authorino stores resolved identity fields in Envoy's filter metadata
    /// under its filter name. In proxy-wasm the path is:
    ///   `["metadata", "filter_metadata", "<authz_filter_name>", key]`
    fn get_authorino_metadata(&self, key: &str) -> Option<String> {
        let path = vec![
            "metadata",
            "filter_metadata",
            self.authz_filter_name.as_str(),
            key,
        ];
        match self.get_property(path) {
            Ok(Some(bytes)) => String::from_utf8(bytes).ok(),
            _ => None,
        }
    }

    /// Determine the failure status for an incomplete or abnormal request.
    ///
    /// Returns one of: "upstream_error", "client_disconnect", or "ok".
    fn detect_failure_status(&self) -> String {
        // A 5xx from the upstream is an upstream error regardless of stream state.
        if let Some(code) = self.upstream_status_code {
            if code >= 500 {
                return "upstream_error".to_string();
            }
        }

        // If we were streaming and never completed (no [DONE] received), and the
        // upstream was healthy, it's a client disconnect.
        if self.is_streaming {
            return "client_disconnect".to_string();
        }

        "ok".to_string()
    }

    /// Handle a chunk of a streaming (SSE) response body.
    ///
    /// CRITICAL: always returns `Action::Continue` -- the response must never
    /// be buffered or delayed for streaming requests.
    fn handle_streaming_body(&mut self, body_size: usize, end_of_stream: bool) -> Action {
        if let Ok(Some(chunk)) = self.get_http_response_body(0, body_size) {
            if let Ok(text) = String::from_utf8(chunk) {
                self.sse_buffer.push_str(&text);
                self.process_sse_buffer();
            }
        }

        // If the stream ends without [DONE] (abnormal), emit whatever we have.
        if end_of_stream && !self.usage_emitted {
            let status = self.detect_failure_status();
            if let Some(usage) = self.captured_usage.clone() {
                warn!(
                    "capture-filter[{}]: end_of_stream without [DONE], \
                     emitting partial (status={})",
                    self.context_id, status
                );
                self.emit_usage_event(&usage, &status, true);
            }
        }

        if self.dispatch_pending {
            return Action::Pause;
        }
        Action::Continue
    }

    /// Handle a non-streaming response body.
    ///
    /// Buffers with `Action::Pause` until the full body is available, then
    /// parses the JSON and extracts usage. This is safe for non-streaming
    /// because the client is waiting for the complete response anyway.
    fn handle_non_streaming_body(&mut self, body_size: usize, end_of_stream: bool) -> Action {
        if !end_of_stream {
            return Action::Pause;
        }

        if let Ok(Some(body)) = self.get_http_response_body(0, body_size) {
            match serde_json::from_slice::<serde_json::Value>(&body) {
                Ok(json) => {
                    // Extract upstream request id from the response body.
                    if let Some(id) = json.get("id").and_then(|v| v.as_str()) {
                        self.upstream_request_id = Some(id.to_string());
                    }
                    // Extract usage.
                    if let Some(usage_val) = json.get("usage") {
                        match serde_json::from_value::<UsageData>(usage_val.clone()) {
                            Ok(usage) => {
                                self.emit_usage_event(&usage, "ok", false);
                            }
                            Err(e) => {
                                warn!(
                                    "capture-filter[{}]: failed to parse usage \
                                     from non-streaming response: {}",
                                    self.context_id, e
                                );
                            }
                        }
                    }
                }
                Err(e) => {
                    warn!(
                        "capture-filter[{}]: non-streaming response body is not valid JSON: {}",
                        self.context_id, e
                    );
                }
            }
        }

        if self.dispatch_pending {
            return Action::Pause;
        }
        Action::Continue
    }

    /// Drain complete SSE events from the buffer and process each one.
    ///
    /// SSE events are delimited by `\n\n`. A chunk boundary may fall in the
    /// middle of an event, so incomplete events remain in the buffer until the
    /// next chunk arrives.
    fn process_sse_buffer(&mut self) {
        while let Some(pos) = self.sse_buffer.find("\n\n") {
            let event = self.sse_buffer[..pos].to_string();
            self.sse_buffer = self.sse_buffer[pos + 2..].to_string();
            self.handle_sse_event(&event);
        }
    }

    /// Process a single SSE event (the text between `\n\n` delimiters).
    ///
    /// OpenAI SSE format:
    /// ```text
    /// data: {"id":"chatcmpl-...","choices":[...]}\n\n
    /// data: {"id":"chatcmpl-...","choices":[],"usage":{...}}\n\n
    /// data: [DONE]\n\n
    /// ```
    fn handle_sse_event(&mut self, event: &str) {
        for line in event.lines() {
            let data = match line.strip_prefix("data: ") {
                Some(d) => d,
                None => continue,
            };

            if data == "[DONE]" {
                self.on_stream_done();
                return;
            }

            // Try to parse the data payload as JSON.
            let chunk: serde_json::Value = match serde_json::from_str(data) {
                Ok(v) => v,
                Err(_) => continue,
            };

            // Capture the upstream request id (same across all chunks).
            if let Some(id) = chunk.get("id").and_then(|v| v.as_str()) {
                self.upstream_request_id = Some(id.to_string());
            }

            // The usage-bearing chunk has `"choices": []` and contains `"usage"`.
            // It appears right before `data: [DONE]`.
            if let Some(usage_val) = chunk.get("usage") {
                if !usage_val.is_null() {
                    if let Ok(u) = serde_json::from_value::<UsageData>(usage_val.clone()) {
                        debug!(
                            "capture-filter[{}]: captured streaming usage: \
                             prompt={} completion={} total={}",
                            self.context_id,
                            u.prompt_tokens,
                            u.completion_tokens,
                            u.total_tokens
                        );
                        self.captured_usage = Some(u);
                    }
                }
            }
        }
    }

    /// Called when `data: [DONE]` is received -- the SSE stream is complete.
    fn on_stream_done(&mut self) {
        if let Some(usage) = self.captured_usage.clone() {
            self.emit_usage_event(&usage, "ok", false);
        } else {
            warn!(
                "capture-filter[{}]: stream ended with [DONE] but no usage chunk was captured \
                 (upstream may not support include_usage)",
                self.context_id
            );
        }
    }

    /// Construct a usage.v1 event and dispatch it to ledger-svc via
    /// `dispatch_http_call`. The call is fire-and-forget from the request
    /// path's perspective; the `on_http_call_response` callback logs the
    /// result.
    ///
    /// Dedup safety: the `record_id` is the upstream x-request-id (or a
    /// context-derived fallback). If the filter restarts and re-processes a
    /// request, the same `record_id` is sent. The ledger-svc upserts
    /// idempotently on `record_id` + `content_hash`, so duplicates are safe.
    fn emit_usage_event(&mut self, usage: &UsageData, status: &str, partial: bool) {
        if self.usage_emitted {
            return;
        }
        self.usage_emitted = true;

        // Clone fields into locals to avoid borrow conflicts with trait methods.
        let record_id = self
            .upstream_request_id
            .clone()
            .unwrap_or_else(|| format!("capture-{}", self.context_id));
        let tenant_id = self
            .tenant_id
            .clone()
            .unwrap_or_else(|| "unknown".to_string());
        let key_id = self.key_id.clone().unwrap_or_default();
        let model = self
            .model
            .clone()
            .unwrap_or_else(|| "unknown".to_string());
        let is_streaming = self.is_streaming;
        let context_id = self.context_id;
        let ledger_cluster = self.ledger_cluster.clone();

        let occurred_at = self
            .get_current_time()
            .map(format_rfc3339)
            .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_string());

        // Build the usage.v1 envelope per contracts/usage.v1.schema.json.
        let event = serde_json::json!({
            "schema_version": "usage.v1",
            "record_id": record_id,
            "occurred_at": occurred_at,
            "tenant_id": tenant_id,
            "key_id": key_id,
            "source": {
                "profile": "maas",
                "collector": "capture-wasm",
                "collector_version": COLLECTOR_VERSION
            },
            "sku": model,
            "quantities": {
                "prompt_tokens": usage.prompt_tokens,
                "completion_tokens": usage.completion_tokens,
                "total_tokens": usage.total_tokens,
                "reasoning_tokens": usage.reasoning_tokens(),
                "cached_tokens": usage.cached_tokens()
            },
            "status": status,
            "partial": partial,
            "stream": is_streaming
        });

        // Wrap in the ingest endpoint's expected envelope.
        let ingest_body = serde_json::json!({ "event_json": event.to_string() });
        let body = match serde_json::to_vec(&ingest_body) {
            Ok(b) => b,
            Err(e) => {
                error!(
                    "capture-filter[{}]: failed to serialize usage event: {}",
                    context_id, e
                );
                return;
            }
        };

        // The cluster name must match the Envoy cluster; the authority must
        // be a valid hostname (no pipe characters from Istio's cluster naming).
        let authority = ledger_cluster
            .split("||")
            .nth(1)
            .unwrap_or("ledger-svc");
        let result = self.dispatch_http_call(
            &ledger_cluster,
            vec![
                (":method", "POST"),
                (":path", "/v1/ingest"),
                (":authority", authority),
                ("content-type", "application/json"),
            ],
            Some(&body),
            vec![],
            Duration::from_secs(5),
        );

        match result {
            Ok(token) => {
                self.dispatch_pending = true;
                info!(
                    "capture-filter[{}]: dispatched usage.v1 to {} \
                     (call_token={}, record_id={}, tokens={})",
                    context_id, ledger_cluster, token, record_id, usage.total_tokens
                );
            }
            Err(e) => {
                error!(
                    "capture-filter[{}]: failed to dispatch usage event: {:?}",
                    context_id, e
                );
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Format a `SystemTime` as RFC 3339 (UTC, second precision).
///
/// Uses Howard Hinnant's `civil_from_days` algorithm to convert epoch seconds
/// to a calendar date without pulling in a datetime crate.
fn format_rfc3339(time: SystemTime) -> String {
    let duration = time.duration_since(UNIX_EPOCH).unwrap_or_default();
    let secs = duration.as_secs();

    let days = (secs / 86400) as i64;
    let time_of_day = secs % 86400;
    let hours = time_of_day / 3600;
    let minutes = (time_of_day % 3600) / 60;
    let seconds = time_of_day % 60;

    // civil_from_days: convert days since 1970-01-01 to (year, month, day).
    let z = days + 719_468;
    let era = (if z >= 0 { z } else { z - 146_096 }) / 146_097;
    let doe = (z - era * 146_097) as u64;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y = yoe as i64 + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = if m <= 2 { y + 1 } else { y };

    format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}Z",
        y, m, d, hours, minutes, seconds
    )
}
