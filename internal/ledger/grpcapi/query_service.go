package grpcapi

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	ledgerv1 "github.com/rdwj/platform-billing-operator/gen/go/ledger/v1"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type QueryServiceServer struct {
	ledgerv1.UnimplementedQueryServiceServer
	querier *ledger.Querier
}

func NewQueryServiceServer(querier *ledger.Querier) *QueryServiceServer {
	return &QueryServiceServer{querier: querier}
}

func (s *QueryServiceServer) QueryEvents(ctx context.Context, req *ledgerv1.QueryEventsRequest) (*ledgerv1.QueryEventsResponse, error) {
	filter := ledger.QueryFilter{
		TenantID:  req.TenantId,
		SKU:       req.Sku,
		Limit:     req.Limit,
		PageToken: req.PageToken,
	}
	if req.StartTime != nil {
		filter.StartTime = req.StartTime.AsTime()
	}
	if req.EndTime != nil {
		filter.EndTime = req.EndTime.AsTime()
	}

	result, err := s.querier.QueryEvents(ctx, filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "query failed: %v", err)
	}

	pbEvents := make([]*ledgerv1.UsageEvent, 0, len(result.Events))
	for i := range result.Events {
		pbEv, err := toProtoEvent(&result.Events[i])
		if err != nil {
			return nil, status.Errorf(codes.Internal, "marshal event: %v", err)
		}
		pbEvents = append(pbEvents, pbEv)
	}

	return &ledgerv1.QueryEventsResponse{
		Events:        pbEvents,
		NextPageToken: result.NextPageToken,
	}, nil
}

func toProtoEvent(ev *ledger.UsageEvent) (*ledgerv1.UsageEvent, error) {
	quantitiesJSON, err := json.Marshal(ev.Quantities)
	if err != nil {
		return nil, fmt.Errorf("marshal quantities: %w", err)
	}
	attributesJSON, err := json.Marshal(ev.Attributes)
	if err != nil {
		return nil, fmt.Errorf("marshal attributes: %w", err)
	}

	pb := &ledgerv1.UsageEvent{
		RecordId:         ev.RecordID,
		SchemaVer:        ev.SchemaVer,
		TenantId:         ev.TenantID,
		KeyId:            ev.KeyID,
		Sku:              ev.SKU,
		Geo:              ev.Geo,
		Profile:          ev.Profile,
		Collector:        ev.Collector,
		PromptTokens:     ev.PromptTokens,
		CompletionTokens: ev.CompletionTokens,
		ReasoningTokens:  ev.ReasoningTokens,
		CachedTokens:     ev.CachedTokens,
		QuantitiesJson:   string(quantitiesJSON),
		Status:           ev.Status,
		Partial:          ev.Partial,
		AttributesJson:   string(attributesJSON),
		ContentHash:      ev.ContentHash,
		EventTs:          timestamppb.New(ev.EventTS),
		IngestedAt:       timestamppb.New(ev.IngestedAt),
	}
	if ev.Stream != nil {
		pb.Stream = *ev.Stream
	}
	if ev.LatencyMs != nil {
		pb.LatencyMs = *ev.LatencyMs
	}
	return pb, nil
}
