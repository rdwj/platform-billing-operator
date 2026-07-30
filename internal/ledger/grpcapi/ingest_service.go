package grpcapi

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ledgerv1 "github.com/rdwj/platform-billing-operator/gen/go/ledger/v1"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type IngestServiceServer struct {
	ledgerv1.UnimplementedIngestServiceServer
	ingester *ledger.Ingester
}

func NewIngestServiceServer(ingester *ledger.Ingester) *IngestServiceServer {
	return &IngestServiceServer{ingester: ingester}
}

func (s *IngestServiceServer) IngestEvent(ctx context.Context, req *ledgerv1.IngestEventRequest) (*ledgerv1.IngestEventResponse, error) {
	if req.EventJson == "" {
		return nil, status.Error(codes.InvalidArgument, "event_json is required")
	}

	result, err := s.ingester.IngestEvent(ctx, json.RawMessage(req.EventJson))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ingest failed: %v", err)
	}

	return &ledgerv1.IngestEventResponse{
		Result:  mapIngestResult(result),
		Message: string(result),
	}, nil
}

func mapIngestResult(r ledger.IngestResult) ledgerv1.IngestResult {
	switch r {
	case ledger.Inserted:
		return ledgerv1.IngestResult_INGEST_RESULT_INSERTED
	case ledger.Duplicate:
		return ledgerv1.IngestResult_INGEST_RESULT_DUPLICATE
	case ledger.ConflictDLQ:
		return ledgerv1.IngestResult_INGEST_RESULT_CONFLICT_DLQ
	case ledger.SchemaUnknownDLQ:
		return ledgerv1.IngestResult_INGEST_RESULT_SCHEMA_UNKNOWN_DLQ
	case ledger.ValidationFailed:
		return ledgerv1.IngestResult_INGEST_RESULT_VALIDATION_FAILED
	default:
		return ledgerv1.IngestResult_INGEST_RESULT_UNSPECIFIED
	}
}
