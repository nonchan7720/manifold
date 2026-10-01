// Package queue implements toolmetrics.Publisher on top of queue services.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
)

// sqsMaxBatchEntries is SendMessageBatch's hard per-request entry limit.
const sqsMaxBatchEntries = 10

// SQSSendMessageBatchAPI is the subset of *sqs.Client SQSPublisher uses.
type SQSSendMessageBatchAPI interface {
	SendMessageBatch(
		ctx context.Context, params *sqs.SendMessageBatchInput, optFns ...func(*sqs.Options),
	) (*sqs.SendMessageBatchOutput, error)
}

// SQSPublisher sends each event as one SQS message whose body is the
// event's JSON, using SendMessageBatch in chunks of up to 10.
type SQSPublisher struct {
	client         SQSSendMessageBatchAPI
	queueURL       string
	messageGroupID string
}

var _ toolmetrics.Publisher = (*SQSPublisher)(nil)

// NewSQSPublisher builds a publisher for queueURL. A non-empty
// messageGroupID targets a FIFO queue: it is set on every message and the
// event ID is used as the MessageDeduplicationId.
func NewSQSPublisher(
	client SQSSendMessageBatchAPI, queueURL, messageGroupID string,
) *SQSPublisher {
	return &SQSPublisher{client: client, queueURL: queueURL, messageGroupID: messageGroupID}
}

func (p *SQSPublisher) Publish(ctx context.Context, events []toolmetrics.Event) error {
	var errs []error
	for start := 0; start < len(events); start += sqsMaxBatchEntries {
		end := min(start+sqsMaxBatchEntries, len(events))
		if err := p.sendBatch(ctx, events[start:end]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *SQSPublisher) sendBatch(ctx context.Context, events []toolmetrics.Event) error {
	entries := make([]types.SendMessageBatchRequestEntry, 0, len(events))
	for i, e := range events {
		body, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal tool metrics event: %w", err)
		}
		entry := types.SendMessageBatchRequestEntry{
			Id:          aws.String(strconv.Itoa(i)),
			MessageBody: aws.String(string(body)),
		}
		if p.messageGroupID != "" {
			entry.MessageGroupId = aws.String(p.messageGroupID)
			entry.MessageDeduplicationId = aws.String(e.ID)
		}
		entries = append(entries, entry)
	}

	out, err := p.client.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
		QueueUrl: aws.String(p.queueURL),
		Entries:  entries,
	})
	if err != nil {
		return fmt.Errorf("sqs SendMessageBatch: %w", err)
	}
	if len(out.Failed) > 0 {
		f := out.Failed[0]
		return fmt.Errorf("sqs SendMessageBatch: %d of %d entries failed (first: %s: %s)",
			len(out.Failed), len(entries), aws.ToString(f.Code), aws.ToString(f.Message))
	}
	return nil
}

func (p *SQSPublisher) Close() error { return nil }
