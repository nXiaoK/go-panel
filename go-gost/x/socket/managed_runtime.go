package socket

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/go-gost/x/config"
	"github.com/go-gost/x/registry"
)

const (
	maxManagedRuntimeItems   = 10000
	maxManagedRuntimePayload = 2 << 20
)

var (
	managedServiceName = regexp.MustCompile(`^[1-9][0-9]{0,18}_[1-9][0-9]{0,18}_(?:0|[1-9][0-9]{0,18})_(?:tcp|udp|tls)$`)
	managedChainName   = regexp.MustCompile(`^[1-9][0-9]{0,18}_[1-9][0-9]{0,18}_(?:0|[1-9][0-9]{0,18})_chains$`)
)

type reconcileManagedRuntimeRequest struct {
	Services []string `json:"services"`
	Chains   []string `json:"chains"`
}

type managedRuntimePlan struct {
	staleServices []string
	staleChains   []string
}

func decodeReconcileManagedRuntimeRequest(data interface{}) (reconcileManagedRuntimeRequest, error) {
	var req reconcileManagedRuntimeRequest
	raw, err := json.Marshal(data)
	if err != nil {
		return req, err
	}
	if len(raw) > maxManagedRuntimePayload {
		return req, errors.New("managed runtime payload too large")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	return req, nil
}

func planManagedRuntimeReconciliation(localServices, localChains []string, desired reconcileManagedRuntimeRequest) (managedRuntimePlan, error) {
	if len(desired.Services)+len(desired.Chains) > maxManagedRuntimeItems {
		return managedRuntimePlan{}, errors.New("too many desired managed runtime items")
	}
	desiredServices, err := validateManagedNames(desired.Services, managedServiceName, "desired service")
	if err != nil {
		return managedRuntimePlan{}, err
	}
	desiredChains, err := validateManagedNames(desired.Chains, managedChainName, "desired chain")
	if err != nil {
		return managedRuntimePlan{}, err
	}
	localManagedServices := filterManagedNames(localServices, managedServiceName)
	localManagedChains := filterManagedNames(localChains, managedChainName)
	if len(localManagedServices)+len(localManagedChains) > maxManagedRuntimeItems {
		return managedRuntimePlan{}, errors.New("too many local managed runtime items")
	}
	plan := managedRuntimePlan{}
	for name := range localManagedServices {
		if _, ok := desiredServices[name]; !ok {
			plan.staleServices = append(plan.staleServices, name)
		}
	}
	for name := range localManagedChains {
		if _, ok := desiredChains[name]; !ok {
			plan.staleChains = append(plan.staleChains, name)
		}
	}
	sort.Strings(plan.staleServices)
	sort.Strings(plan.staleChains)
	return plan, nil
}

func validateManagedNames(names []string, grammar *regexp.Regexp, kind string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		if !grammar.MatchString(name) {
			return nil, fmt.Errorf("invalid %s name %q", kind, name)
		}
		if _, duplicate := out[name]; duplicate {
			return nil, fmt.Errorf("duplicate %s name %q", kind, name)
		}
		out[name] = struct{}{}
	}
	return out, nil
}

func filterManagedNames(names []string, grammar *regexp.Regexp) map[string]struct{} {
	out := make(map[string]struct{})
	for _, name := range names {
		if grammar.MatchString(name) {
			out[name] = struct{}{}
		}
	}
	return out
}

func reconcileManagedRuntime(req reconcileManagedRuntimeRequest) error {
	cfg := config.Global()
	localServices := make([]string, 0, len(cfg.Services))
	for _, service := range cfg.Services {
		if service != nil {
			localServices = append(localServices, service.Name)
		}
	}
	for name := range registry.ServiceRegistry().GetAll() {
		localServices = append(localServices, name)
	}
	localChains := make([]string, 0, len(cfg.Chains))
	for _, chain := range cfg.Chains {
		if chain != nil {
			localChains = append(localChains, chain.Name)
		}
	}
	for name := range registry.ChainRegistry().GetAll() {
		localChains = append(localChains, name)
	}
	plan, err := planManagedRuntimeReconciliation(localServices, localChains, req)
	if err != nil {
		return err
	}
	// Separate registry and config-only objects. A previous interrupted update
	// can leave either side behind; both are panel-managed only after the strict
	// grammar filter above, so reconciling one must not prevent stale objects on
	// the other side from being removed.
	configServices := make(map[string]struct{}, len(cfg.Services))
	for _, service := range cfg.Services {
		if service != nil {
			configServices[service.Name] = struct{}{}
		}
	}
	configChains := make(map[string]struct{}, len(cfg.Chains))
	for _, chain := range cfg.Chains {
		if chain != nil {
			configChains[chain.Name] = struct{}{}
		}
	}
	configOnlyServices := make([]string, 0)
	registeredServices := make([]string, 0, len(plan.staleServices))
	for _, name := range plan.staleServices {
		if registry.ServiceRegistry().Get(name) != nil {
			registeredServices = append(registeredServices, name)
		} else if _, ok := configServices[name]; ok {
			configOnlyServices = append(configOnlyServices, name)
		}
	}
	configOnlyChains := make([]string, 0)
	registeredChains := make([]string, 0, len(plan.staleChains))
	for _, name := range plan.staleChains {
		if registry.ChainRegistry().IsRegistered(name) {
			registeredChains = append(registeredChains, name)
		} else if _, ok := configChains[name]; ok {
			configOnlyChains = append(configOnlyChains, name)
		}
	}
	// Services may reference chains, so remove services first.
	if len(registeredServices) > 0 {
		if err := deleteServices(deleteServicesRequest{Services: registeredServices}); err != nil {
			return err
		}
	}
	for _, name := range registeredChains {
		if err := deleteChain(deleteChainRequest{Chain: name}); err != nil {
			return err
		}
	}
	if len(configOnlyServices) > 0 || len(configOnlyChains) > 0 {
		if err := removeManagedRuntimeConfig(configOnlyServices, configOnlyChains); err != nil {
			return err
		}
	}
	return nil
}

func removeManagedRuntimeConfig(serviceNames, chainNames []string) error {
	services := make(map[string]struct{}, len(serviceNames))
	for _, name := range serviceNames {
		services[name] = struct{}{}
	}
	chains := make(map[string]struct{}, len(chainNames))
	for _, name := range chainNames {
		chains[name] = struct{}{}
	}
	return config.OnUpdate(func(c *config.Config) error {
		keptServices := c.Services[:0]
		for _, service := range c.Services {
			if service == nil {
				keptServices = append(keptServices, service)
				continue
			}
			if _, stale := services[service.Name]; !stale {
				keptServices = append(keptServices, service)
			}
		}
		c.Services = keptServices
		keptChains := c.Chains[:0]
		for _, chain := range c.Chains {
			if chain == nil {
				keptChains = append(keptChains, chain)
				continue
			}
			if _, stale := chains[chain.Name]; !stale {
				keptChains = append(keptChains, chain)
			}
		}
		c.Chains = keptChains
		return nil
	})
}
