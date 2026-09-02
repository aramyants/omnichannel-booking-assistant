package cloudtasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/idtoken"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/reminders"
)

var taskNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

type fakeCreator struct {
	request *cloudtaskspb.CreateTaskRequest
	err     error
}

func (f *fakeCreator) CreateTask(
	_ context.Context,
	req *cloudtaskspb.CreateTaskRequest,
	_ ...gax.CallOption,
) (*cloudtaskspb.Task, error) {
	f.request = req
	return req.Task, f.err
}

func taskConfig() Config {
	return Config{
		ProjectID:           "emotion-concept",
		Location:            "europe-west1",
		Queue:               "appointment-reminders",
		TargetURL:           "https://booking.example.run.app/tasks/reminders",
		Audience:            "https://booking.example.run.app",
		ServiceAccountEmail: "runtime@emotion-concept.iam.gserviceaccount.com",
	}
}

func TestScheduleCreatesAnAuthenticatedNamedTask(t *testing.T) {
	creator := &fakeCreator{}
	scheduler := newScheduler(creator, nil, taskConfig())
	scheduler.now = func() time.Time { return taskNow }
	runAt := taskNow.Add(48 * time.Hour)

	err := scheduler.Schedule(t.Context(), reminders.Task{
		ID: "reminder-task-a1b2", ReminderID: "reminder-c3d4", RunAt: runAt,
	})
	if err != nil {
		t.Fatalf("Schedule() returned error: %v", err)
	}
	req := creator.request
	if req.Parent != "projects/emotion-concept/locations/europe-west1/queues/appointment-reminders" {
		t.Errorf("parent = %q", req.Parent)
	}
	if req.Task.Name != req.Parent+"/tasks/reminder-task-a1b2" {
		t.Errorf("task name = %q", req.Task.Name)
	}
	httpReq := req.Task.GetHttpRequest()
	if httpReq.GetUrl() != taskConfig().TargetURL ||
		httpReq.GetOidcToken().GetAudience() != taskConfig().Audience ||
		httpReq.GetOidcToken().GetServiceAccountEmail() != taskConfig().ServiceAccountEmail {
		t.Errorf("HTTP task = %+v", httpReq)
	}
	var body struct {
		ReminderID string `json:"reminder_id"`
	}
	if err := json.Unmarshal(httpReq.GetBody(), &body); err != nil || body.ReminderID != "reminder-c3d4" {
		t.Errorf("body = %q, error = %v", httpReq.GetBody(), err)
	}
	if !req.Task.ScheduleTime.AsTime().Equal(runAt) {
		t.Errorf("schedule time = %s, want %s", req.Task.ScheduleTime.AsTime(), runAt)
	}
}

func TestDuplicateTaskIsSuccessfulIdempotence(t *testing.T) {
	creator := &fakeCreator{err: status.Error(codes.AlreadyExists, "task exists")}
	scheduler := newScheduler(creator, nil, taskConfig())
	scheduler.now = func() time.Time { return taskNow }

	err := scheduler.Schedule(t.Context(), reminders.Task{
		ID: "same-task", ReminderID: "same-reminder", RunAt: taskNow.Add(time.Hour),
	})
	if err != nil {
		t.Errorf("Schedule() = %v, want duplicate accepted", err)
	}
}

func TestScheduleRefusesCloudTasksThirtyDayLimit(t *testing.T) {
	scheduler := newScheduler(&fakeCreator{}, nil, taskConfig())
	scheduler.now = func() time.Time { return taskNow }
	err := scheduler.Schedule(t.Context(), reminders.Task{
		ID: "too-far", ReminderID: "reminder", RunAt: taskNow.Add(31 * 24 * time.Hour),
	})
	if err == nil {
		t.Error("Schedule() accepted a task beyond Cloud Tasks' maximum horizon")
	}
}

func TestAuthorizerChecksAudienceAndServiceAccount(t *testing.T) {
	authorizer, err := NewAuthorizer(taskConfig().Audience, taskConfig().ServiceAccountEmail)
	if err != nil {
		t.Fatal(err)
	}
	var gotAudience string
	authorizer.validate = func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
		if token != "signed-token" {
			t.Errorf("token = %q", token)
		}
		gotAudience = audience
		return &idtoken.Payload{Claims: map[string]any{
			"email": taskConfig().ServiceAccountEmail,
		}}, nil
	}

	if err := authorizer.Authorize(t.Context(), "Bearer signed-token"); err != nil {
		t.Errorf("Authorize() returned error: %v", err)
	}
	if gotAudience != taskConfig().Audience {
		t.Errorf("validated audience = %q", gotAudience)
	}

	authorizer.validate = func(context.Context, string, string) (*idtoken.Payload, error) {
		return &idtoken.Payload{Claims: map[string]any{"email": "attacker@example.com"}}, nil
	}
	if err := authorizer.Authorize(t.Context(), "Bearer signed-token"); err == nil {
		t.Error("Authorize() trusted a different service account")
	}
	authorizer.validate = func(context.Context, string, string) (*idtoken.Payload, error) {
		return nil, errors.New("bad signature")
	}
	if err := authorizer.Authorize(t.Context(), "Bearer signed-token"); err == nil {
		t.Error("Authorize() trusted an invalid token")
	}
}
