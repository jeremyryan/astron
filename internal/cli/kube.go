/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// kubeOptions holds the flags used to locate and select a kubeconfig context.
type kubeOptions struct {
	// kubeconfig is an explicit path to a kubeconfig file. When empty, the
	// standard loading rules (KUBECONFIG env, then ~/.kube/config) are used.
	kubeconfig string
	// context selects a named context from the kubeconfig. When empty, the
	// kubeconfig's current-context is used.
	context string
}

// restConfig builds a *rest.Config from the kube options, honouring the
// standard kubeconfig loading rules and an optional explicit path/context.
func (o *kubeOptions) restConfig() (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if o.kubeconfig != "" {
		loadingRules.ExplicitPath = o.kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if o.context != "" {
		overrides.CurrentContext = o.context
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	// Suppress server-sent API deprecation warnings; they are noise for a
	// manifest generator and would otherwise be logged to stderr.
	cfg.WarningHandler = rest.NoWarnings{}
	return cfg, nil
}
