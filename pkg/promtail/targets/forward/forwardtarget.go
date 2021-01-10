package forward

import (
	"strings"
	"sync"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/relabel"

	"github.com/grafana/loki/pkg/logproto"
	"github.com/grafana/loki/pkg/logql"
	"github.com/grafana/loki/pkg/promtail/api"
	"github.com/grafana/loki/pkg/promtail/forwarder"
	"github.com/grafana/loki/pkg/promtail/scrapeconfig"
	"github.com/grafana/loki/pkg/promtail/targets/target"
)

const (
	// ForwardTargetType is a Loki forward target
	ForwardTargetType = target.TargetType("forward")
)

type ForwardTarget struct {
	logger        log.Logger
	handler       api.EntryHandler
	config        *scrapeconfig.ForwardTargetConfig
	relabelConfig []*relabel.Config
	jobName       string
	client        *forwarder.Client
	logChan       chan logproto.PushRequest
	once          sync.Once
	wg            sync.WaitGroup
}

func NewForwardTarget(logger log.Logger,
	handler api.EntryHandler,
	relabel []*relabel.Config,
	jobName string,
	config *scrapeconfig.ForwardTargetConfig) (*ForwardTarget, error) {
	t := &ForwardTarget{
		logger:        logger,
		handler:       handler,
		relabelConfig: relabel,
		jobName:       jobName,
		config:        config,
		logChan:       make(chan logproto.PushRequest),
	}

	t.client = forwarder.NewClient(logger, config.Client, t.handlerPushRequest)
	err := t.client.Start()
	if err != nil {
		return nil, err
	}

	t.wg.Add(1)
	go t.handle()
	return t, nil
}

func (t *ForwardTarget) handlerPushRequest(data *logproto.PushRequest) {
	t.logChan <- *data
}

func (t *ForwardTarget) handle() {
	defer t.wg.Done()
	for req := range t.logChan {
		var lastErr error
		for _, stream := range req.Streams {
			ls, err := logql.ParseLabels(stream.Labels)
			if err != nil {
				lastErr = err
				continue
			}

			lb := labels.NewBuilder(ls)

			// Add configured labels
			for k, v := range t.config.Labels {
				lb.Set(string(k), string(v))
			}

			// Apply relabeling
			processed := relabel.Process(lb.Labels(), t.relabelConfig...)
			if processed == nil || len(processed) == 0 {
				return
			}

			// Convert to model.LabelSet
			filtered := model.LabelSet{}
			for i := range processed {
				if strings.HasPrefix(processed[i].Name, "__") {
					continue
				}
				filtered[model.LabelName(processed[i].Name)] = model.LabelValue(processed[i].Value)
			}

			for _, entry := range stream.Entries {
				e := api.Entry{
					Labels: filtered.Clone(),
					Entry: logproto.Entry{
						Line: entry.Line,
					},
				}
				if t.config.KeepTimestamp {
					e.Timestamp = entry.Timestamp
				} else {
					e.Timestamp = time.Now()
				}
				t.handler.Chan() <- e
			}
		}

		if lastErr != nil {
			level.Warn(t.logger).Log("msg", "at least one entry in the push request failed to process", "err", lastErr.Error())
			return
		}
	}
}

// Type returns ForwardTargetType.
func (t *ForwardTarget) Type() target.TargetType {
	return ForwardTargetType
}

// Ready indicates whether or not the ForwardTarget target is ready to be read from.
func (t *ForwardTarget) Ready() bool {
	return true
}

// DiscoveredLabels returns the set of labels discovered by the ForwardTarget, which
// is always nil. Implements Target.
func (t *ForwardTarget) DiscoveredLabels() model.LabelSet {
	return nil
}

// Labels returns the set of labels that statically apply to all log entries
// produced by the ForwardTarget.
func (t *ForwardTarget) Labels() model.LabelSet {
	return t.config.Labels
}

// Details returns target-specific details.
func (t *ForwardTarget) Details() interface{} {
	return map[string]string{}
}

// Stop shuts down the ForwardTarget.
func (t *ForwardTarget) Stop() error {
	level.Info(t.logger).Log("msg", "stopping push server", "job", t.jobName)
	t.once.Do(func() { close(t.logChan) })
	t.wg.Wait()
	t.client.Stop()
	t.handler.Stop()
	return nil
}
