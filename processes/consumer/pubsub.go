package consumer

import (
	gcp_pubsub "cloud.google.com/go/pubsub"
	"context"
	"fmt"
	"github.com/artie-labs/transfer/lib/artie"
	"github.com/artie-labs/transfer/lib/cdc/format"
	"github.com/artie-labs/transfer/lib/config"
	"github.com/artie-labs/transfer/lib/jitter"
	"github.com/artie-labs/transfer/lib/logger"
	"google.golang.org/api/option"
	"sync"
	"time"
)

const defaultAckDeadline = 5 * time.Minute

func findOrCreateSubscription(ctx context.Context, client *gcp_pubsub.Client, topic, subName string) (*gcp_pubsub.Subscription, error) {
	log := logger.FromContext(ctx)
	sub := client.Subscription(subName)
	exists, err := sub.Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subscription, err: %v", err)
	}

	if !exists {
		log.WithField("topic", topic).Info("subscription does not exist, creating one...")
		gcpTopic := client.Topic(topic)
		exists, err = gcpTopic.Exists(ctx)
		if !exists || err != nil {
			// We error out if the topic does not exist or there's an error.
			return nil, fmt.Errorf("failed to fetch gcp topic, exists: %v, err: %v", exists, err)
		}

		sub, err = client.CreateSubscription(ctx, subName, gcp_pubsub.SubscriptionConfig{
			Topic:       gcpTopic,
			AckDeadline: defaultAckDeadline,
			// Enable ordering given the `partition key` which is known as ordering key in Pub/Sub
			EnableMessageOrdering: true,
		})

		if err != nil {
			return nil, fmt.Errorf("failed to create subscription, topic: %s, err: %v", topic, err)
		}
	}

	return sub, err
}

func StartSubscriber(ctx context.Context, flushChan chan bool) {
	log := logger.FromContext(ctx)
	settings := config.FromContext(ctx)
	client, clientErr := gcp_pubsub.NewClient(ctx, settings.Config.Pubsub.ProjectID,
		option.WithCredentialsFile(settings.Config.Pubsub.PathToCredentials))
	if clientErr != nil {
		log.Fatalf("failed to create a pubsub client, err: %v", clientErr)
	}

	topicToConfigFmtMap := make(map[string]TopicConfigFormatter)
	var topics []string
	for _, topicConfig := range settings.Config.Pubsub.TopicConfigs {
		topicToConfigFmtMap[topicConfig.Topic] = TopicConfigFormatter{
			tc:     topicConfig,
			Format: format.GetFormatParser(ctx, topicConfig.CDCFormat),
		}
		topics = append(topics, topicConfig.Topic)
	}

	var wg sync.WaitGroup
	for _, topicConfig := range settings.Config.Pubsub.TopicConfigs {
		wg.Add(1)
		go func(ctx context.Context, client *gcp_pubsub.Client, topic string) {
			defer wg.Done()
			subName := fmt.Sprintf("transfer_%s", topic)
			sub, err := findOrCreateSubscription(ctx, client, topic, subName)
			if err != nil {
				log.Fatalf("failed to find or create subscription, err: %v", err)
			}

			err = sub.Receive(ctx, func(_ context.Context, pubsubMsg *gcp_pubsub.Message) {
				msg := artie.NewMessage(nil, pubsubMsg, topic)
				msg.EmitIngestionLag(ctx, subName)
				logFields := map[string]interface{}{
					"topic": msg.Topic(),
					"msgID": msg.PubSub.ID,
					"key":   string(msg.Key()),
					"value": string(msg.Value()),
				}

				shouldFlush, processErr := processMessage(ctx, msg, topicToConfigFmtMap, subName)
				if processErr != nil {
					log.WithError(processErr).WithFields(logFields).Warn("skipping message...")
				}

				if shouldFlush {
					flushChan <- true
					// Jitter-sleep is necessary to allow the flush process to acquire the table lock
					// If it doesn't then the flush process may be over-exhausted since the lock got acquired by `processMessage(...)`.
					// This then leads us to make unnecessary flushes.
					jitterDuration := jitter.JitterMs(500, 1)
					time.Sleep(time.Duration(jitterDuration) * time.Millisecond)
				}
			})

			if err != nil {
				log.Fatalf("sub receive error, err: %v", err)
			}
		}(ctx, client, topicConfig.Topic)

	}

	wg.Wait()
}
