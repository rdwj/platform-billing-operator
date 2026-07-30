package grpcapi

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	ledgerv1 "github.com/rdwj/platform-billing-operator/gen/go/ledger/v1"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

func NewServer(ingester *ledger.Ingester, querier *ledger.Querier) *grpc.Server {
	srv := grpc.NewServer()

	ledgerv1.RegisterIngestServiceServer(srv, NewIngestServiceServer(ingester))
	ledgerv1.RegisterQueryServiceServer(srv, NewQueryServiceServer(querier))

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("ledger.v1.IngestService", healthpb.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("ledger.v1.QueryService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, healthSrv)

	reflection.Register(srv)
	return srv
}
