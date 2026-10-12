package queue

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	streamWork      = "WORK"
	streamDLQ       = "DLQ"
	streamBroadcast = "BROADCAST"
)

const (
	jobSubjectPrefix       = "jobs."
	dlqSubjectPrefix       = "dlq."
	broadcastSubjectPrefix = "broadcast."
	allSubjects            = ">"

	namePattern = `^[a-z0-9_]+(\.[a-z0-9_]+)+$`
)

func subjectFor(prefix, name string, version int) string {
	return prefix + name + ".v" + strconv.Itoa(version)
}

func dlqSubject(subject string) string { return dlqSubjectPrefix + subject }

func durableName(subject string) string {
	return strings.ReplaceAll(strings.TrimPrefix(subject, jobSubjectPrefix), ".", "_")
}

func nameProblem(name string, version int) string {
	if ok, err := regexp.MatchString(namePattern, name); err != nil || !ok {
		return "name must match " + namePattern
	}
	if version < 1 {
		return "version must be at least 1"
	}
	return ""
}

func streamConfigs() []jetstream.StreamConfig {
	return []jetstream.StreamConfig{
		{
			Name:       streamWork,
			Subjects:   []string{jobSubjectPrefix + allSubjects},
			Retention:  jetstream.WorkQueuePolicy,
			Storage:    jetstream.FileStorage,
			Replicas:   streamReplicas,
			MaxAge:     workMaxAge,
			MaxBytes:   streamMaxBytes,
			Discard:    jetstream.DiscardNew,
			Duplicates: duplicateWindow,
		},
		{
			Name:       streamDLQ,
			Subjects:   []string{dlqSubjectPrefix + jobSubjectPrefix + allSubjects},
			Retention:  jetstream.LimitsPolicy,
			Storage:    jetstream.FileStorage,
			Replicas:   streamReplicas,
			MaxAge:     dlqMaxAge,
			MaxBytes:   streamMaxBytes,
			Discard:    jetstream.DiscardOld,
			Duplicates: duplicateWindow,
		},
		{
			Name:       streamBroadcast,
			Subjects:   []string{broadcastSubjectPrefix + allSubjects},
			Retention:  jetstream.LimitsPolicy,
			Storage:    jetstream.FileStorage,
			Replicas:   streamReplicas,
			MaxAge:     broadcastMaxAge,
			MaxBytes:   broadcastMaxBytes,
			Discard:    jetstream.DiscardOld,
			Duplicates: duplicateWindow,
		},
	}
}

func consumerConfig(j Job) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:       durableName(j.Subject()),
		FilterSubject: j.Subject(),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       AckWait,
		MaxDeliver:    consumerMaxDeliver,
		MaxAckPending: consumerMaxAckPending,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}
}

func ensureStreams(ctx context.Context, js jetstream.JetStream) error {
	for _, cfg := range streamConfigs() {
		if _, err := js.CreateOrUpdateStream(ctx, cfg); err != nil {
			return fmt.Errorf("queue: ensure stream %s: %w", cfg.Name, err)
		}
	}
	return nil
}
