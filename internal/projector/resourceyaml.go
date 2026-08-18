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

package projector

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// ErrLiveResourceUnavailable indicates the projector has no dynamic client or
// RESTMapper configured, so live resource reads (ResourceYAML) are
// unavailable. Projectors are normally constructed with both; this only
// arises in restricted test/embedding setups.
var ErrLiveResourceUnavailable = errors.New("live resource fetching is not configured")

// lastAppliedAnnotation is the noisy kubectl annotation stripped from
// ResourceYAML output, matching the API server's /api/resource endpoint.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// ResourceYAML fetches a single live resource from the cluster this
// projection watches and renders it as YAML, with noisy server-managed fields
// (managedFields, the last-applied-configuration annotation) stripped for
// readability. It uses the same dynamic client and RESTMapper the
// projector's informers are built from.
func (p *Projector) ResourceYAML(ctx context.Context, apiVersion, kind, namespace, name string) ([]byte, error) {
	if p.opts.Dynamic == nil || p.opts.Mapper == nil {
		return nil, ErrLiveResourceUnavailable
	}

	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}
	gvk := gv.WithKind(kind)
	mapping, err := p.opts.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", gvk, err)
	}

	var ri dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ri = p.opts.Dynamic.Resource(mapping.Resource).Namespace(namespace)
	} else {
		ri = p.opts.Dynamic.Resource(mapping.Resource)
	}

	obj, err := ri.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%s %q not found: %w", kind, name, err)
		}
		return nil, err
	}

	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
	if ann := obj.GetAnnotations(); ann != nil {
		delete(ann, lastAppliedAnnotation)
		if len(ann) == 0 {
			unstructured.RemoveNestedField(obj.Object, "metadata", "annotations")
		} else {
			obj.SetAnnotations(ann)
		}
	}

	out, err := yaml.Marshal(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("marshaling YAML: %w", err)
	}
	return out, nil
}
