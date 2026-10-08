package firestore

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/reminder"
)

// retryingReminderServer reproduces a real Firestore transaction conflict:
// the first callback prepares a claim, its commit is aborted, and a retried
// callback observes the reminder already claimed or finished by another worker.
// Using the real SDK's retry loop makes this regression run without an emulator.
type retryingReminderServer struct {
	firestorepb.UnimplementedFirestoreServer
	at          time.Time
	retryStatus reminder.Status
	attempts    atomic.Int32
}

func (s *retryingReminderServer) BeginTransaction(context.Context, *firestorepb.BeginTransactionRequest) (*firestorepb.BeginTransactionResponse, error) {
	return &firestorepb.BeginTransactionResponse{Transaction: []byte(fmt.Sprint(s.attempts.Add(1)))}, nil
}

func (s *retryingReminderServer) BatchGetDocuments(req *firestorepb.BatchGetDocumentsRequest, stream firestorepb.Firestore_BatchGetDocumentsServer) error {
	state, claimID := reminder.StatusScheduled, ""
	if string(req.GetTransaction()) != "1" {
		state, claimID = s.retryStatus, "other-worker"
	}
	return stream.Send(&firestorepb.BatchGetDocumentsResponse{
		Result: &firestorepb.BatchGetDocumentsResponse_Found{Found: &firestorepb.Document{
			Name:       req.Documents[0],
			CreateTime: timestamppb.New(s.at),
			UpdateTime: timestamppb.New(s.at),
			Fields: map[string]*firestorepb.Value{
				"id":            {ValueType: &firestorepb.Value_StringValue{StringValue: "reminder"}},
				"status":        {ValueType: &firestorepb.Value_StringValue{StringValue: string(state)}},
				"claim_id":      {ValueType: &firestorepb.Value_StringValue{StringValue: claimID}},
				"claim_expires": {ValueType: &firestorepb.Value_TimestampValue{TimestampValue: timestamppb.New(s.at.Add(time.Minute))}},
			},
		}},
		ReadTime: timestamppb.New(s.at),
	})
}

func (s *retryingReminderServer) Commit(_ context.Context, req *firestorepb.CommitRequest) (*firestorepb.CommitResponse, error) {
	if string(req.Transaction) == "1" {
		return nil, status.Error(codes.Aborted, "another worker won the claim")
	}
	return &firestorepb.CommitResponse{CommitTime: timestamppb.New(s.at)}, nil
}

func TestReminderClaimOwnershipUsesFinalTransactionAttempt(t *testing.T) {
	for _, state := range []reminder.Status{reminder.StatusDelivering, reminder.StatusSent} {
		t.Run(string(state), func(t *testing.T) {
			listener := bufconn.Listen(1 << 20)
			srv := grpc.NewServer()
			backend := &retryingReminderServer{at: testNow, retryStatus: state}
			firestorepb.RegisterFirestoreServer(srv, backend)
			go func() { _ = srv.Serve(listener) }()
			t.Cleanup(srv.Stop)
			t.Cleanup(func() { _ = listener.Close() })
			conn, err := grpc.NewClient("passthrough:///firestore-test",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			client, err := firestore.NewClient(t.Context(), "test-project", option.WithGRPCConn(conn))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			store := &Store{client: client}
			got, owned, err := store.ClaimReminder(ctx, "reminder", "this-worker", testNow, testNow.Add(5*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if backend.attempts.Load() != 2 {
				t.Fatalf("SDK ran %d attempts, want the conflicting commit to be retried", backend.attempts.Load())
			}
			if owned || got.ClaimID != "other-worker" || got.Status != state {
				t.Fatalf("a losing transaction reported ownership: owned=%t reminder=%+v", owned, got)
			}
		})
	}
}
