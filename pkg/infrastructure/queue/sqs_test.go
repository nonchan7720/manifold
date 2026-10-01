package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
	"github.com/stretchr/testify/require"
)

type fakeSQS struct {
	inputs []*sqs.SendMessageBatchInput
	out    *sqs.SendMessageBatchOutput
	err    error
}

func (f *fakeSQS) SendMessageBatch(
	_ context.Context, in *sqs.SendMessageBatchInput, _ ...func(*sqs.Options),
) (*sqs.SendMessageBatchOutput, error) {
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	if f.out != nil {
		return f.out, nil
	}
	return &sqs.SendMessageBatchOutput{}, nil
}

func events(n int) []toolmetrics.Event {
	es := make([]toolmetrics.Event, n)
	for i := range es {
		es[i] = toolmetrics.Event{
			ID:     string(rune('a' + i)),
			Tool:   "t",
			Status: toolmetrics.StatusSuccess,
		}
	}
	return es
}

func TestSQSPublisher_ChunksIntoBatchesOfTen(t *testing.T) {
	f := &fakeSQS{}
	p := NewSQSPublisher(f, "https://sqs.example/queue", "")

	require.NoError(t, p.Publish(t.Context(), events(11)))
	require.Len(t, f.inputs, 2)
	require.Len(t, f.inputs[0].Entries, 10)
	require.Len(t, f.inputs[1].Entries, 1)
	require.Equal(t, "https://sqs.example/queue", aws.ToString(f.inputs[0].QueueUrl))

	var got toolmetrics.Event
	require.NoError(
		t,
		json.Unmarshal([]byte(aws.ToString(f.inputs[0].Entries[0].MessageBody)), &got),
	)
	require.Equal(t, "a", got.ID)
	require.Nil(t, f.inputs[0].Entries[0].MessageGroupId, "standard queue sets no group")
}

func TestSQSPublisher_FIFOSetsGroupAndDeduplicationID(t *testing.T) {
	f := &fakeSQS{}
	p := NewSQSPublisher(f, "https://sqs.example/queue.fifo", "manifold")

	require.NoError(t, p.Publish(t.Context(), events(2)))
	entry := f.inputs[0].Entries[1]
	require.Equal(t, "manifold", aws.ToString(entry.MessageGroupId))
	require.Equal(t, "b", aws.ToString(entry.MessageDeduplicationId))
}

func TestSQSPublisher_ReportsFailedEntries(t *testing.T) {
	f := &fakeSQS{out: &sqs.SendMessageBatchOutput{
		Failed: []types.BatchResultErrorEntry{{Id: aws.String("0"), Code: aws.String("Throttled")}},
	}}
	err := NewSQSPublisher(f, "q", "").Publish(t.Context(), events(1))
	require.ErrorContains(t, err, "1 of 1 entries failed")
	require.ErrorContains(t, err, "Throttled")
}

func TestSQSPublisher_ContinuesAfterFailedChunk(t *testing.T) {
	f := &fakeSQS{err: errors.New("boom")}
	err := NewSQSPublisher(f, "q", "").Publish(t.Context(), events(15))
	require.ErrorContains(t, err, "boom")
	require.Len(t, f.inputs, 2, "every chunk is attempted")
}
