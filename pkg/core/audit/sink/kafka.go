package sink

import event "github.com/bhangun/iket/pkg/core/audit"

// KafkaSink writes audit events to Kafka (placeholder)

type KafkaSink struct {
	name   string
	topic  string
	broker string
}

func NewKafkaSink(broker, topic string) (*KafkaSink, error) {
	return &KafkaSink{
		name:   "kafka",
		topic:  topic,
		broker: broker,
	}, nil
}

func (s *KafkaSink) Name() string {
	return s.name
}

func (s *KafkaSink) Write(event *event.Event) error {
	// In production, send to Kafka
	return nil
}

func (s *KafkaSink) Close() error {
	return nil
}
