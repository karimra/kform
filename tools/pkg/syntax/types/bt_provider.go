package types

import (
	"context"
	"fmt"

	"github.com/henderiw-nephio/kform/kform-sdk-go/pkg/diag"
	blockv1alpha1 "github.com/henderiw-nephio/kform/tools/apis/kform/block/v1alpha1"
	"github.com/henderiw-nephio/kform/tools/pkg/syntax/exttypes"
	"github.com/henderiw-nephio/kform/tools/pkg/syntax/sctx"
	"github.com/henderiw-nephio/kform/tools/pkg/util/cctx"
)

type provider struct{ bt }

func newProvider(n string) Block {
	return &provider{
		bt{
			Level: 1,
			Name:  n,
		},
	}
}

func (r *provider) AddData(ctx context.Context) {
	provider := cctx.GetContextValue[string](ctx, sctx.CtxKeyVarName)
	attrs := cctx.GetContextValue[map[string]any](ctx, sctx.CtxKeyAttributes)
	for k, v := range attrs {
		if k == "alias" {
			if alias, ok := v.(string); ok {
				provider = fmt.Sprintf("%s.%s", provider, alias)
			}
		}
	}

	execCfg := cctx.GetContextValue[exttypes.ExecConfig](ctx, sctx.CtxExecConfig)
	if execCfg == nil {
		r.recorder.Record(diag.DiagErrorf("cannot add provider without execConfig"))
	}

	if err := execCfg.GetProviders().AddVertex(ctx, provider, blockv1alpha1.Provider{
		FileName:   cctx.GetContextValue[string](ctx, sctx.CtxKeyFileName),
		BlockType:  blockv1alpha1.BlockTypeProvider,
		Attributes: attrs,
		Object:     cctx.GetContextValue[any](ctx, sctx.CtxKeyObject),
	}); err != nil {
		v, _ := execCfg.GetProviders().GetVertex(provider)
		r.recorder.Record(diag.DiagFromErrWithContext(sctx.GetContext(ctx), fmt.Errorf("duplicate resource with fileName: %s, name: %s, type: %s", v.FileName, provider, string(v.BlockType))))
	}
}
