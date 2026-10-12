package boot

import (
	"fmt"
	"log/slog"

	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/todo"
)

func consumerDomains() []string { return []string{"todo"} }

func consumerProviders(s *Services, log *slog.Logger) []queue.Provider {
	return []queue.Provider{
		todo.NewConsumer(s.Todos, log),
	}
}

func listenerProviders(gate *onboardingGate) []queue.ListenerProvider {
	return []queue.ListenerProvider{
		gate,
	}
}

func assertConsumerWiring(ps []queue.Provider) error {
	domains := consumerDomains()
	if len(ps) != len(domains) {
		return fmt.Errorf("consumer wiring: %d providers for %d domains %v", len(ps), len(domains), domains)
	}
	missing := make([]string, 0, len(ps))
	for i, p := range ps {
		if p == nil {
			missing = append(missing, domains[i])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("consumer wiring: domains missing a provider: %v", missing)
	}
	return nil
}

func handlersOf(ps []queue.Provider) []queue.Handler {
	var hs []queue.Handler
	for _, p := range ps {
		hs = append(hs, p.ConsumerHandlers()...)
	}
	return hs
}

func listenersOf(ps []queue.ListenerProvider) []queue.Listener {
	var ls []queue.Listener
	for _, p := range ps {
		ls = append(ls, p.Listeners()...)
	}
	return ls
}

func jobsOf(hs []queue.Handler) []queue.Job {
	jobs := make([]queue.Job, 0, len(hs))
	for _, h := range hs {
		jobs = append(jobs, h.Job)
	}
	return jobs
}

func broadcastsOf(ls []queue.Listener) []queue.Broadcast {
	bs := make([]queue.Broadcast, 0, len(ls))
	for _, l := range ls {
		bs = append(bs, l.Broadcast)
	}
	return bs
}
