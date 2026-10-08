package service

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
)

type CreateNotificationInput struct {
	Event    domain.InboxEvent
	Payload  []byte
	Channels []domain.Channel
}

type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type InboxRepo interface {
	Claim(ctx context.Context, event domain.InboxEvent) error
}

type NotificationRepo interface {
	CreateForChannels(ctx context.Context, notification domain.Notification, channels []domain.Channel) error
}

type NotificationServiceDeps struct {
	Tx            TxManager
	Inbox         InboxRepo
	Notifications NotificationRepo
}

type NotificationService struct {
	tx            TxManager
	inbox         InboxRepo
	notifications NotificationRepo
}

func NewNotificationService(deps NotificationServiceDeps) *NotificationService {
	return &NotificationService{
		tx:            deps.Tx,
		inbox:         deps.Inbox,
		notifications: deps.Notifications,
	}
}

func (uc *NotificationService) CreateNotification(ctx context.Context, input CreateNotificationInput) error {
	return uc.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := uc.inbox.Claim(ctx, input.Event); err != nil {
			return err
		}

		return uc.notifications.CreateForChannels(ctx, domain.Notification{
			EventID:   input.Event.EventID,
			EventType: input.Event.EventType,
			Payload:   input.Payload,
		}, input.Channels)
	})
}
