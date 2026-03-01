package kafka

import (
	"fmt"

	kafkago "github.com/segmentio/kafka-go"
)

func NewConn(broker string) (*kafkago.Conn, error) {
	conn, err := kafkago.Dial("tcp", broker)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	if _, err := conn.Brokers(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("brokers: %w", err)
	}

	return conn, nil
}
