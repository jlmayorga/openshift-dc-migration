package main

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type jsonOnlySerializer struct{ runtime.NegotiatedSerializer }

func (s jsonOnlySerializer) SupportedMediaTypes() []runtime.SerializerInfo {
	for _, info := range s.NegotiatedSerializer.SupportedMediaTypes() {
		if info.MediaType == "application/json" {
			return []runtime.SerializerInfo{info}
		}
	}
	return nil
}

func newJSONDynamicClient(config *rest.Config) (dynamic.Interface, error) {
	// Upstream ConfigFor can override explicit content types through ambient
	// CBOR feature flags. Reassert the baseline protocol after those defaults,
	// then construct the unversioned client without reapplying them.
	cfg := dynamic.ConfigFor(config)
	cfg.ContentType, cfg.AcceptContentTypes = "application/json", "application/json"
	cfg.NegotiatedSerializer = jsonOnlySerializer{cfg.NegotiatedSerializer}
	cfg.GroupVersion, cfg.APIPath = nil, ""
	client, err := rest.UnversionedRESTClientFor(cfg)
	if err != nil {
		return nil, err
	}
	return dynamic.New(client), nil
}
