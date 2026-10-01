// Package queue はキューサービスを使った toolmetrics.Publisher の実装。
package queue

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
)

// sqsMaxBatchEntries は SendMessageBatch の 1 リクエストあたりのエントリ数上限。
const sqsMaxBatchEntries = 10

// SQS メッセージ属性の名前。
const (
	// SQSAttributeSchema は本文の型の完全修飾名（toolmetrics.SchemaName）。
	SQSAttributeSchema = "schema"
	// SQSAttributeContentType は本文の形式（toolmetrics.ContentType）。
	SQSAttributeContentType = "contentType"
)

// SQSSendMessageBatchAPI は SQSPublisher が使う *sqs.Client のメソッド。
type SQSSendMessageBatchAPI interface {
	SendMessageBatch(
		ctx context.Context, params *sqs.SendMessageBatchInput, optFns ...func(*sqs.Options),
	) (*sqs.SendMessageBatchOutput, error)
}

// SQSPublisher はイベント 1 件を、本文がイベントの protojson である SQS メッセージ
// 1 件として送る。SendMessageBatch で最大 10 件ずつ送信する。メッセージ属性
// SQSAttributeSchema / SQSAttributeContentType に本文のスキーマ名と形式を付ける。
type SQSPublisher struct {
	client         SQSSendMessageBatchAPI
	queueURL       string
	messageGroupID string
}

var _ toolmetrics.Publisher = (*SQSPublisher)(nil)

// NewSQSPublisher は queueURL 宛ての Publisher を作る。messageGroupID が空でない
// 場合は FIFO キュー向けとして全メッセージに設定し、イベント ID を
// MessageDeduplicationId に使う。
func NewSQSPublisher(
	client SQSSendMessageBatchAPI, queueURL, messageGroupID string,
) *SQSPublisher {
	return &SQSPublisher{client: client, queueURL: queueURL, messageGroupID: messageGroupID}
}

func (p *SQSPublisher) Publish(ctx context.Context, events []*toolmetrics.Event) error {
	var errs []error
	for start := 0; start < len(events); start += sqsMaxBatchEntries {
		end := min(start+sqsMaxBatchEntries, len(events))
		if err := p.sendBatch(ctx, events[start:end]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *SQSPublisher) sendBatch(ctx context.Context, events []*toolmetrics.Event) error {
	entries := make([]types.SendMessageBatchRequestEntry, 0, len(events))
	for i, e := range events {
		body, err := toolmetrics.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal tool metrics event: %w", err)
		}
		entry := types.SendMessageBatchRequestEntry{
			Id:          aws.String(strconv.Itoa(i)),
			MessageBody: aws.String(string(body)),
			MessageAttributes: map[string]types.MessageAttributeValue{
				SQSAttributeSchema: {
					DataType: aws.String("String"), StringValue: aws.String(toolmetrics.SchemaName),
				},
				SQSAttributeContentType: {
					DataType: aws.String(
						"String",
					),
					StringValue: aws.String(toolmetrics.ContentType),
				},
			},
		}
		if p.messageGroupID != "" {
			entry.MessageGroupId = aws.String(p.messageGroupID)
			entry.MessageDeduplicationId = aws.String(e.GetId())
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
