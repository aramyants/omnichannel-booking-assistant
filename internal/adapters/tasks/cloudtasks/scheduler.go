// Package cloudtasks adapts Google Cloud Tasks to the reminder scheduler port.
package cloudtasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cloudtaskapi "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/reminders"
)

const maximumScheduleDelay = 30 * 24 * time.Hour

type taskCreator interface {
	CreateTask(
		context.Context,
		*cloudtaskspb.CreateTaskRequest,
		...gax.CallOption,
	) (*cloudtaskspb.Task, error)
}

// Config identifies one queue and its authenticated HTTP target.
type Config struct {
	ProjectID           string
	Location            string
	Queue               string
	TargetURL           string
	Audience            string
	ServiceAccountEmail string
}

// Scheduler creates authenticated HTTP tasks.
type Scheduler struct {
	client              taskCreator
	close               func() error
	parent              string
	targetURL           string
	audience            string
	serviceAccountEmail string
	now                 func() time.Time
}

// New connects to Cloud Tasks. The caller owns the returned Close.
func New(ctx context.Context, cfg Config) (*Scheduler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	client, err := cloudtaskapi.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloud tasks: connect: %w", err)
	}
	return newScheduler(client, client.Close, cfg), nil
}

func newScheduler(client taskCreator, closeClient func() error, cfg Config) *Scheduler {
	return &Scheduler{
		client:              client,
		close:               closeClient,
		parent:              fmt.Sprintf("projects/%s/locations/%s/queues/%s", cfg.ProjectID, cfg.Location, cfg.Queue),
		targetURL:           cfg.TargetURL,
		audience:            cfg.Audience,
		serviceAccountEmail: cfg.ServiceAccountEmail,
		now:                 time.Now,
	}
}

func (c Config) validate() error {
	switch {
	case c.ProjectID == "":
		return errors.New("cloud tasks: project id is required")
	case c.Location == "":
		return errors.New("cloud tasks: location is required")
	case c.Queue == "":
		return errors.New("cloud tasks: queue is required")
	case !strings.HasPrefix(c.TargetURL, "https://"):
		return errors.New("cloud tasks: target URL must use https")
	case c.Audience == "":
		return errors.New("cloud tasks: OIDC audience is required")
	case c.ServiceAccountEmail == "":
		return errors.New("cloud tasks: service account email is required")
	}
	return nil
}

// Schedule creates one named task. ALREADY_EXISTS means the same deterministic
// task was already planned and is successful idempotence, not an error.
func (s *Scheduler) Schedule(ctx context.Context, task reminders.Task) error {
	if task.ID == "" || task.ReminderID == "" || task.RunAt.IsZero() {
		return errors.New("cloud tasks: task is incomplete")
	}
	if task.RunAt.After(s.now().Add(maximumScheduleDelay)) {
		return fmt.Errorf("cloud tasks: run time exceeds the %s scheduling limit", maximumScheduleDelay)
	}

	body, err := json.Marshal(struct {
		ReminderID string `json:"reminder_id"`
	}{ReminderID: task.ReminderID})
	if err != nil {
		return fmt.Errorf("cloud tasks: encode reminder payload: %w", err)
	}
	scheduleTime := timestamppb.New(task.RunAt)
	if err := scheduleTime.CheckValid(); err != nil {
		return fmt.Errorf("cloud tasks: invalid run time: %w", err)
	}

	_, err = s.client.CreateTask(ctx, &cloudtaskspb.CreateTaskRequest{
		Parent: s.parent,
		Task: &cloudtaskspb.Task{
			Name: s.parent + "/tasks/" + task.ID,
			MessageType: &cloudtaskspb.Task_HttpRequest{HttpRequest: &cloudtaskspb.HttpRequest{
				Url:        s.targetURL,
				HttpMethod: cloudtaskspb.HttpMethod_POST,
				Headers:    map[string]string{"Content-Type": "application/json"},
				Body:       body,
				AuthorizationHeader: &cloudtaskspb.HttpRequest_OidcToken{OidcToken: &cloudtaskspb.OidcToken{
					ServiceAccountEmail: s.serviceAccountEmail,
					Audience:            s.audience,
				}},
			}},
			ScheduleTime: scheduleTime,
		},
	})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cloud tasks: create task: %w", err)
	}
	return nil
}

// Close releases the Cloud Tasks client connection.
func (s *Scheduler) Close() error {
	if s.close == nil {
		return nil
	}
	return s.close()
}
