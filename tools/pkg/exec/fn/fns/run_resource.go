package fns

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/henderiw-nephio/kform/tools/pkg/exec/fn"
	"github.com/henderiw-nephio/kform/tools/pkg/exec/vars"
	"github.com/henderiw-nephio/kform/tools/pkg/exec/vctx"
	"github.com/henderiw-nephio/kform/tools/pkg/syntax/types"
	"github.com/henderiw-nephio/kform/tools/pkg/util/cache"
	"github.com/henderiw/logger/log"
)

func NewResourceFn(cfg *Config) fn.BlockInstanceRunner {
	return &resource{
		vars: cfg.Vars,
	}
}

type resource struct {
	vars cache.Cache[vars.Variable]
}

func (r *resource) Run(ctx context.Context, vCtx *types.VertexContext, localVars map[string]any) error {
	// NOTE: forEach or count expected and its respective values will be represented in localVars
	// ForEach: each.key/value
	// Count: count.index

	log := log.FromContext(ctx).With("vertexContext", vctx.GetContext(vCtx))
	log.Info("run instance")

	// 1. render the config of the resource with variable subtitution
	if vCtx.BlockContext.Config == nil {
		// Pressence of the config should be checked in the syntax validation
		return fmt.Errorf("cannot run without config for %s", vctx.GetContext(vCtx))
	}
	renderer := &Renderer{Vars: r.vars}
	d, err := renderer.RenderConfig(ctx, vCtx.BlockName, vCtx.BlockContext.Config, localVars)
	if err != nil {
		return fmt.Errorf("cannot render config for %s", vctx.GetContext(vCtx))
	}
	if vCtx.BlockContext.Attributes.Schema == nil {
		return fmt.Errorf("cannot add type meta without a schema for %s", vctx.GetContext(vCtx))
	}
	d, err = AddTypeMeta(ctx, *vCtx.BlockContext.Attributes.Schema, d)
	if err != nil {
		return fmt.Errorf("cannot add type meta for %s, err: %s", vctx.GetContext(vCtx), err.Error())
	}
	fmt.Println(d)

	b, err := json.Marshal(d)
	if err != nil {
		log.Error("cannot json marshal list", "error", err.Error())
		return err
	}
	log.Info("data", "req", string(b))

	// 2. run provider
	// lookup the provider in the provider instances
	// based on the blockType run either data or resource
	// add the data in the variable
	fmt.Println("provider", vCtx.Provider)

	/*
		
		resp, err := provider.CreateResource(ctx, &kfplugin1.CreateResource_Request{
			Name: "resourcebackend_ipclaim",
			Data: readByte,
		})
		if err != nil {
			log.Error("cannot read resource", "error", err.Error())
			return err
		}
		if diag.Diagnostics(resp.Diagnostics).HasError() {
			log.Error("request failed", "error", diag.Diagnostics(resp.Diagnostics).Error())
			return err
		}

		if err := json.Unmarshal(resp.Data, ipClaim); err != nil {
			log.Error("cannot unmarshal read resp", "error", err.Error())
			return err
		}
		log.Info("response", "ipClaim", ipClaim)
	*/

	return nil
}
