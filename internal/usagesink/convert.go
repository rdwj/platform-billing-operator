/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package usagesink

import (
	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/shopspring/decimal"
)

// ConvertLedgerEvent maps a ledger.UsageEvent to a usagesink.UsageEvent.
func ConvertLedgerEvent(ev ledger.UsageEvent) UsageEvent {
	metadata := make(map[string]string, len(ev.Attributes))
	for k, v := range ev.Attributes {
		metadata[k] = v
	}

	return UsageEvent{
		RecordID:         ev.RecordID,
		TenantID:         ev.TenantID,
		Timestamp:        ev.EventTS,
		EventType:        ev.Profile,
		Model:            ev.SKU,
		PromptTokens:     decimal.NewFromInt(ev.PromptTokens),
		CompletionTokens: decimal.NewFromInt(ev.CompletionTokens),
		ReasoningTokens:  decimal.NewFromInt(ev.ReasoningTokens),
		CachedTokens:     decimal.NewFromInt(ev.CachedTokens),
		RequestID:        ev.RecordID,
		Metadata:         metadata,
	}
}

// ConvertLedgerEvents maps a slice of ledger events to usagesink events.
func ConvertLedgerEvents(events []ledger.UsageEvent) []UsageEvent {
	out := make([]UsageEvent, len(events))
	for i, ev := range events {
		out[i] = ConvertLedgerEvent(ev)
	}
	return out
}
