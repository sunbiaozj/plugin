package retrieve

import (
	"github.com/33cn/chain33/pluginmgr"
	"github.com/33cn/plugin/plugin/dapp/retrieve/commands"
	"github.com/33cn/plugin/plugin/dapp/retrieve/executor"
	"github.com/33cn/plugin/plugin/dapp/retrieve/rpc"
	"github.com/33cn/plugin/plugin/dapp/retrieve/types"
)

func init() {
	pluginmgr.Register(&pluginmgr.PluginBase{
		Name:     types.RetrieveX,
		ExecName: executor.GetName(),
		Exec:     executor.Init,
		Cmd:      commands.RetrieveCmd,
		RPC:      rpc.Init,
	})
}