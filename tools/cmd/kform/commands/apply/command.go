package apply

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	docs "github.com/henderiw-nephio/kform/internal/docs/generated/applydocs"
	"github.com/henderiw-nephio/kform/kform-sdk-go/pkg/diag"
	"github.com/henderiw-nephio/kform/tools/pkg/exec/fn/fns"
	"github.com/henderiw-nephio/kform/tools/pkg/exec/record"
	"github.com/henderiw-nephio/kform/tools/pkg/exec/vars"
	"github.com/henderiw-nephio/kform/tools/pkg/fsys"
	"github.com/henderiw-nephio/kform/tools/pkg/pkgio"
	"github.com/henderiw-nephio/kform/tools/pkg/recorder"
	"github.com/henderiw-nephio/kform/tools/pkg/syntax/parser"
	"github.com/henderiw-nephio/kform/tools/pkg/syntax/types"
	"github.com/henderiw-nephio/kform/tools/pkg/util/cache"
	"github.com/henderiw/logger/log"
	"github.com/spf13/cobra"
)

// NewRunner returns a command runner.
func NewRunner(ctx context.Context, version string) *Runner {
	r := &Runner{}
	cmd := &cobra.Command{
		Use:     "apply [flags]",
		Args:    cobra.ExactArgs(1),
		Short:   docs.ApplyShort,
		Long:    docs.ApplyShort + "\n" + docs.ApplyLong,
		Example: docs.ApplyExamples,
		RunE:    r.runE,
	}

	r.Command = cmd

	r.Command.Flags().BoolVar(
		&r.AutoApprove, "auto-approve", false, "skip interactive approval of plan before applying")

	return r
}

func NewCommand(ctx context.Context, version string) *cobra.Command {
	return NewRunner(ctx, version).Command
}

type Runner struct {
	Command     *cobra.Command
	rootPath    string
	AutoApprove bool
}

func (r *Runner) runE(c *cobra.Command, args []string) error {
	ctx := c.Context()
	log := log.FromContext(ctx)

	r.rootPath = args[0]
	// validate the rootpath, so far we assume we run a directory calling the main function
	// but not within the main fn
	if err := fsys.ValidateDirPath(r.rootPath); err != nil {
		return err
	}
	// check if the root path exists
	_, err := os.Stat(r.rootPath)
	if err != nil {
		return fmt.Errorf("cannot init kform, path does not exist: %s", r.rootPath)
	}

	// initialize the recorder
	parserecorder := recorder.New[diag.Diagnostic]()
	ctx = context.WithValue(ctx, types.CtxKeyRecorder, parserecorder)

	// syntax check config -> build the dag
	log.Info("parsing modules")
	p, err := parser.NewKformParser(ctx, r.rootPath)
	if err != nil {
		return err
	}
	p.Parse(ctx)
	if parserecorder.Get().HasError() {
		parserecorder.Print()
		log.Error("failed parsing modules", "error", parserecorder.Get().Error())
		return parserecorder.Get().Error()
	}
	parserecorder.Print()
	providerInventory, err := p.InitProviderInventory(ctx)
	if err != nil {
		log.Error("failed initializing provider inventory", "error", err)
		return err
	}
	providerInstances := p.InitProviderInstances(ctx)

	rm, err := p.GetRootModule(ctx)
	if err != nil {
		log.Error("failed parsing no root module found")
		return fmt.Errorf("failed parsing no root module found")
	}

	runrecorder := recorder.New[record.Record]()
	varsCache := cache.New[vars.Variable]()

	// run the provider DAG
	log.Info("create provider runner")
	rmfn := fns.NewModuleFn(&fns.Config{
		Provider:          true,
		RootModuleName:    rm.NSN.Name,
		Vars:              varsCache,
		Recorder:          runrecorder,
		ProviderInstances: providerInstances,
		ProviderInventory: providerInventory,
	})
	log.Info("executing provider runner DAG")
	if err := rmfn.Run(ctx, &types.VertexContext{
		FileName:     filepath.Join(r.rootPath, pkgio.PkgFileMatch[0]),
		ModuleName:   rm.NSN.Name,
		BlockType:    types.BlockTypeModule,
		BlockName:    rm.NSN.Name,
		DAG:          rm.ProviderDAG, // we supply the provider DAG here
		BlockContext: types.KformBlockContext{},
	}, map[string]any{}); err != nil {
		log.Error("failed running provider DAG", "err", err)
		return err
	}
	log.Info("success executing provider DAG")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		fmt.Println("context Done")
		for nsn, provider := range providerInstances.List() {
			log.Info("closing provider", "nsn", nsn)
			if provider != nil {
				log.Info("closing provider", "nsn", nsn)
				provider.Close(ctx)
			}
		}

	}()

	/*
		// initialize the providers -> provider factory
		providerInventory, err := providers.Initialize(ctx, r.rootPath, p.GetProviderRequirements(ctx))
		if err != nil {
			log.Error("failed initializing providers", "err", err)
			return fmt.Errorf("failed initializing providers err: %s", err.Error())
		}

		// initialize the provider instances
		for nsn, provConfig := range p.GetProviderConfigs(ctx) {
			log := log.With("nsn", nsn)



			p, err := providerInventory.Get(nsn)
			if err != nil {
				log.Error("provider not found", "nsn", nsn, "err", err)
				return fmt.Errorf("provider not found nsn: %s err: %s", nsn, err.Error())
			}
			provider, err := p.Initializer()
			if err != nil {
				return err
			}
			defer provider.Close(ctx)

			renderer := &fns.Renderer{Vars: cache.New[vars.Variable]()}
			d, err := renderer.RenderConfig(ctx, nsn.Name, provConfig.Config, map[string]any{})
			if err != nil {
				return err
			}
			if provConfig.Attributes != nil && provConfig.Attributes.Schema == nil {
				return fmt.Errorf("cannot add type meta without a schema for %s", nsn.Name)
			}
			d, err = fns.AddTypeMeta(ctx, *provConfig.Attributes.Schema, d)
			if err != nil {
				return fmt.Errorf("cannot add type meta for %s, err: %s", nsn.Name, err.Error())
			}
			providerConfigByte, err := json.Marshal(d)
			if err != nil {
				log.Error("cannot json marshal config", "error", err.Error())
				return err
			}
			log.Info("providerConfig", "config", string(providerConfigByte))

			if nsn.Name == "kubernetes" {
				cfgresp, err := provider.Configure(ctx, &kfplugin1.Configure_Request{
					Config: providerConfigByte,
				})
				if err != nil {
					log.Error("failed to configure provider", "error", err.Error())
					panic(err)
				}
				log.Info("configure response", "nsn", nsn, "diag", cfgresp.Diagnostics)
			} else {
				cfgresp, err := provider.Configure(ctx, &kfplugin1.Configure_Request{
					Config: providerConfigByte,
				})
				if err != nil {
					log.Error("failed to configure provider", "error", err.Error())
					panic(err)
				}
				log.Info("configure response", "nsn", nsn, "diag", cfgresp.Diagnostics)

				ipClaim := ipamv1alpha1.BuildIPClaim(metav1.ObjectMeta{Name: "test"}, ipamv1alpha1.IPClaimSpec{
					Kind:            ipamv1alpha1.PrefixKindNetwork,
					NetworkInstance: corev1.ObjectReference{Name: "test"},
				}, ipamv1alpha1.IPClaimStatus{})
				readByte, err := json.Marshal(ipClaim)
				if err != nil {
					log.Error("cannot json marshal list", "error", err.Error())
					return err
				}
				log.Info("data", "req", string(readByte))

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
			}
		}
	*/
	// execute the dag

	runrecorder = recorder.New[record.Record]()
	varsCache = cache.New[vars.Variable]()

	rmfn = fns.NewModuleFn(&fns.Config{
		RootModuleName:    rm.NSN.Name,
		Vars:              varsCache,
		Recorder:          runrecorder,
		ProviderInstances: providerInstances,
		ProviderInventory: providerInventory,
	})

	log.Info("executing module")
	if err := rmfn.Run(ctx, &types.VertexContext{
		FileName:     filepath.Join(r.rootPath, pkgio.PkgFileMatch[0]),
		ModuleName:   rm.NSN.Name,
		BlockType:    types.BlockTypeModule,
		BlockName:    rm.NSN.Name,
		DAG:          rm.DAG,
		BlockContext: types.KformBlockContext{},
	}, map[string]any{}); err != nil {
		log.Error("failed executing module", "err", err)
		return err
	}
	log.Info("success executing module")

	for nsn, v := range varsCache.List() {
		fmt.Println("nsn", nsn)
		fmt.Println("vars", v)
	}

	// auto-apply -> depends on the flag if we approve the change or not.
	return nil
}
