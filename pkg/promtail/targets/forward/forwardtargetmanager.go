package forward

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/grafana/loki/pkg/logentry/stages"
	"github.com/grafana/loki/pkg/promtail/api"
	"github.com/grafana/loki/pkg/promtail/scrapeconfig"
	"github.com/grafana/loki/pkg/promtail/targets/target"
)

// ForwardTargetManager manages a series of ForwardTargets.
type ForwardTargetManager struct {
	logger  log.Logger
	targets map[string]*ForwardTarget
}

// NewForwardTargetManager creates a new ForwardTargetManager.
func NewForwardTargetManager(
	logger log.Logger,
	client api.EntryHandler,
	scrapeConfigs []scrapeconfig.Config,
) (*ForwardTargetManager, error) {
	tm := &ForwardTargetManager{
		logger:  logger,
		targets: make(map[string]*ForwardTarget),
	}

	if err := validateJobName(scrapeConfigs); err != nil {
		return nil, err
	}

	for _, cfg := range scrapeConfigs {
		registerer := prometheus.DefaultRegisterer
		pipeline, err := stages.NewPipeline(log.With(logger, "component", "forward_pipeline_"+cfg.JobName), cfg.PipelineStages, &cfg.JobName, registerer)
		if err != nil {
			return nil, err
		}

		t, err := NewForwardTarget(logger, pipeline.Wrap(client), cfg.RelabelConfigs, cfg.JobName, cfg.ForwardConfig)
		if err != nil {
			return nil, err
		}

		tm.targets[cfg.JobName] = t
	}

	return tm, nil
}

func validateJobName(scrapeConfigs []scrapeconfig.Config) error {
	jobNames := map[string]struct{}{}
	for i, cfg := range scrapeConfigs {
		if cfg.JobName == "" {
			return errors.New("`job_name` must be defined for the `forward` scrape_config with a " +
				"unique name to properly register metrics, " +
				"at least one `forward` scrape_config has no `job_name` defined")
		}
		if _, ok := jobNames[cfg.JobName]; ok {
			return fmt.Errorf("`job_name` must be unique for each `forward` scrape_config, "+
				"a duplicate `job_name` of %s was found", cfg.JobName)
		}
		jobNames[cfg.JobName] = struct{}{}

		scrapeConfigs[i].JobName = strings.Replace(cfg.JobName, " ", "_", -1)
	}
	return nil
}

// Ready returns true if at least one ForwardTarget is also ready.
func (tm *ForwardTargetManager) Ready() bool {
	for _, t := range tm.targets {
		if t.Ready() {
			return true
		}
	}
	return false
}

// Stop stops the ForwardTargetManager and all of its ForwardTargets.
func (tm *ForwardTargetManager) Stop() {
	for _, t := range tm.targets {
		if err := t.Stop(); err != nil {
			level.Error(t.logger).Log("msg", "error stopping ForwardTarget", "err", err.Error())
		}
	}
}

// ActiveTargets returns the list of ForwardTargets where Forward data
// is being read. ActiveTargets is an alias to AllTargets as
// ForwardTargets cannot be deactivated, only stopped.
func (tm *ForwardTargetManager) ActiveTargets() map[string][]target.Target {
	return tm.AllTargets()
}

// AllTargets returns the list of all targets where forward data
// is currently being read.
func (tm *ForwardTargetManager) AllTargets() map[string][]target.Target {
	result := make(map[string][]target.Target, len(tm.targets))
	for k, v := range tm.targets {
		result[k] = []target.Target{v}
	}
	return result
}
