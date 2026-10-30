package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/service/mocks"
)

// fakeTx выполняет fn сразу и отмечает, что код находится внутри транзакции.
// Если задан err, транзакция не открывается и fn не вызывается.
type fakeTx struct {
	isInTx bool
	err    error
}

func (f *fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if f.err != nil {
		return f.err
	}

	f.isInTx = true
	defer func() { f.isInTx = false }()

	return fn(ctx)
}

func TestNotificationService_CreateNotification(t *testing.T) {
	t.Parallel()

	errTx := errors.New("begin tx failed")
	errClaim := errors.New("claim failed")
	errDuplicate := errors.New("event already processed")
	errCreate := errors.New("create failed")

	input := service.CreateNotificationInput{
		Event: domain.InboxEvent{
			EventID:     "event-1",
			Topic:       "order.events.v1",
			Partition:   2,
			Offset:      42,
			EventType:   domain.EventTypeOrderCreated,
			AggregateID: "order-1",
		},
		Payload:  []byte(`{"order_id":"order-1"}`),
		Channels: []domain.Channel{domain.ChannelLog},
	}
	wantNotification := domain.Notification{
		EventID:   "event-1",
		EventType: domain.EventTypeOrderCreated,
		Payload:   input.Payload,
	}

	tests := []struct {
		name    string
		txErr   error
		setup   func(tx *fakeTx, inbox *mocks.MockInboxRepo, repo *mocks.MockNotificationRepo)
		wantErr error
	}{
		{
			name: "claims event then creates notification inside transaction",
			setup: func(tx *fakeTx, inbox *mocks.MockInboxRepo, repo *mocks.MockNotificationRepo) {
				claim := inbox.EXPECT().Claim(mock.Anything, input.Event).
					Run(func(context.Context, domain.InboxEvent) { assert.True(t, tx.isInTx, "Claim outside tx") }).
					Return(nil).Once()
				repo.EXPECT().CreateForChannels(mock.Anything, wantNotification, input.Channels).
					Run(func(context.Context, domain.Notification, []domain.Channel) {
						assert.True(t, tx.isInTx, "CreateForChannels outside tx")
					}).
					Return(nil).NotBefore(claim).Once()
			},
		},
		{
			name: "does not create notification for duplicate event",
			setup: func(_ *fakeTx, inbox *mocks.MockInboxRepo, _ *mocks.MockNotificationRepo) {
				inbox.EXPECT().Claim(mock.Anything, input.Event).Return(errDuplicate).Once()
			},
			wantErr: errDuplicate,
		},
		{
			name: "propagates claim error without creating notification",
			setup: func(_ *fakeTx, inbox *mocks.MockInboxRepo, _ *mocks.MockNotificationRepo) {
				inbox.EXPECT().Claim(mock.Anything, input.Event).Return(errClaim).Once()
			},
			wantErr: errClaim,
		},
		{
			name: "propagates notification repository error",
			setup: func(_ *fakeTx, inbox *mocks.MockInboxRepo, repo *mocks.MockNotificationRepo) {
				inbox.EXPECT().Claim(mock.Anything, input.Event).Return(nil).Once()
				repo.EXPECT().
					CreateForChannels(mock.Anything, wantNotification, input.Channels).
					Return(errCreate).Once()
			},
			wantErr: errCreate,
		},
		{
			name:    "propagates transaction error without touching repositories",
			txErr:   errTx,
			setup:   func(*fakeTx, *mocks.MockInboxRepo, *mocks.MockNotificationRepo) {},
			wantErr: errTx,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tx := &fakeTx{err: tt.txErr}
			inbox := mocks.NewMockInboxRepo(t)
			repo := mocks.NewMockNotificationRepo(t)
			tt.setup(tx, inbox, repo)

			svc := service.NewNotificationService(service.NotificationServiceDeps{
				Tx:            tx,
				Inbox:         inbox,
				Notifications: repo,
			})

			err := svc.CreateNotification(context.Background(), input)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
