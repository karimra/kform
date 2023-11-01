package fns

import (
	"context"
	"fmt"
	"strings"

	"github.com/henderiw-nephio/kform/tools/pkg/exec/fn"
	"github.com/henderiw-nephio/kform/tools/pkg/exec/fn/render"
	"github.com/henderiw-nephio/kform/tools/pkg/exec/record"
	"github.com/henderiw-nephio/kform/tools/pkg/exec/vars"
	"github.com/henderiw-nephio/kform/tools/pkg/exec/vctx"
	"github.com/henderiw-nephio/kform/tools/pkg/executor"
	"github.com/henderiw-nephio/kform/tools/pkg/recorder"
	"github.com/henderiw-nephio/kform/tools/pkg/util/cache"
	"github.com/henderiw/logger/log"
)

func NewModuleFn(cfg *Config) fn.BlockInstanceRunner {
	return &module{
		vars:     cfg.Vars,
		recorder: cfg.Recorder,
	}
}

type module struct {
	// initialized from the vertexContext
	rootModuleName string
	// dynamic injection required
	vars     cache.Cache[vars.Variable]
	recorder recorder.Recorder[record.Record]
}

/*
1. run for_each or count or a sinlgeton
-> determines how many child modules will be instantiated
-> assign a local variable each.key/value or count.index

Per execution instance (single or range (count/for_each))
1. prepare dynamic input (uses the for_each/count if relevant)
	root module -> input comes from cmdline or environment variables
				-> copy to the vars cache of the child module
	child module -> input comes from the parent modules variable
				-> copy to the vars cache of the child module
2. execute the dag and dedicated vars context

3. if ok copy the output of the vars into the local vars
*/

func (r *module) Run(ctx context.Context, vCtx *vctx.VertexContext, localVars map[string]any) error {
	log := log.FromContext(ctx).With("vertexContext", vctx.GetContext(vCtx))
	log.Info("run instance")
	// render the new vars input
	newvars := cache.New[vars.Variable]()

	// copy the input to the new vars
	if vCtx.BlockContext.InputParams != nil {
		for inputvar, d := range vCtx.BlockContext.InputParams {
			renderer := render.Renderer{
				Vars:      r.vars,
				LocalVars: localVars,
			}
			d, err := renderer.Render(ctx, d)
			if err != nil {
				return fmt.Errorf("run module, render failed for inputVar %s, err: %s", inputvar, err.Error())
			}

			switch d := d.(type) {
			case []any:
				newvars.Add(ctx, cache.NSN{Name: fmt.Sprintf("input.%s", inputvar)}, vars.Variable{Data: map[string][]any{
					vars.DummyKey: d,
				}})
			default:
				newvars.Add(ctx, cache.NSN{Name: fmt.Sprintf("input.%s", inputvar)}, vars.Variable{Data: map[string][]any{
					vars.DummyKey: {d},
				}})
			}
		}
	}
	// prepare and execute the dag
	e := executor.New[*vctx.VertexContext](ctx, vCtx.DAG, &executor.Config[*vctx.VertexContext]{
		Name: vCtx.BlockName,
		Handler: &ExecHandler{
			RootModuleName: r.rootModuleName,
			ModuleName:     vCtx.BlockName,
			FnsMap: NewMap(ctx, &Config{
				Vars:     newvars,
				Recorder: r.recorder,
			}),
			Vars:     newvars,
			Recorder: r.recorder,
		},
	})
	success := e.Run(ctx)
	if success {
		// copy the output to the newvars to the original var
		for nsn, v := range newvars.List() {
			split := strings.Split(nsn.Name, ".")
			if split[0] == "output" {
				if d, ok := v.Data[vars.DummyKey]; ok {
					v, err := r.vars.Get(cache.NSN{Name: vCtx.BlockName})
					if err != nil {
						v = vars.Variable{Data: map[string][]any{}}
					}
					v.Data[split[1]] = d
					r.vars.Upsert(ctx, cache.NSN{Name: vCtx.BlockName}, v)
				}
			}
		}
	}
	return nil
}
