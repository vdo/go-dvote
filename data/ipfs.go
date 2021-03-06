package data

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	files "github.com/ipfs/go-ipfs-files"
	ipfscmds "github.com/ipfs/go-ipfs/commands"
	ipfscore "github.com/ipfs/go-ipfs/core"
	"github.com/ipfs/go-ipfs/core/corehttp"
	"github.com/ipfs/go-ipfs/core/corerepo"
	"github.com/ipfs/go-ipfs/core/coreunix"
	"github.com/ipfs/go-ipfs/repo/fsrepo"
	ipfslog "github.com/ipfs/go-log"
	coreiface "github.com/ipfs/interface-go-ipfs-core"
	"github.com/ipfs/interface-go-ipfs-core/options"
	corepath "github.com/ipfs/interface-go-ipfs-core/path"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	crypto "gitlab.com/vocdoni/go-dvote/crypto/ethereum"
	"gitlab.com/vocdoni/go-dvote/ipfs"
	"gitlab.com/vocdoni/go-dvote/log"
	"gitlab.com/vocdoni/go-dvote/types"
)

const MaxFileSizeBytes = 1024 * 1024 * 50 // 50 MB

type IPFSHandle struct {
	Node     *ipfscore.IpfsNode
	CoreAPI  coreiface.CoreAPI
	DataDir  string
	LogLevel string

	// cancel helps us stop extra goroutines and listeners which complement
	// the IpfsNode above.
	cancel func()
}

func (i *IPFSHandle) Init(d *types.DataStore) error {
	if i.LogLevel == "" {
		i.LogLevel = "ERROR"
	}
	ipfslog.SetLogLevel("*", i.LogLevel)
	ipfs.InstallDatabasePlugins()
	ipfs.ConfigRoot = d.Datadir

	os.Setenv("IPFS_FD_MAX", "1024")

	// check if needs init
	if !fsrepo.IsInitialized(ipfs.ConfigRoot) {
		if err := ipfs.Init(); err != nil {
			log.Errorf("error in IPFS init: %s", err)
		}
	}
	node, coreAPI, err := ipfs.StartNode()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	i.cancel = cancel

	// Start garbage collector, with our cancellable context.
	go corerepo.PeriodicGC(ctx, node)

	log.Infof("IPFS peerID: %s", node.Identity.Pretty())
	// start http
	cctx := ipfs.CmdCtx(node, d.Datadir)
	cctx.ReqLog = &ipfscmds.ReqLog{}

	gatewayOpt := corehttp.GatewayOption(true, corehttp.WebUIPaths...)
	opts := []corehttp.ServeOption{
		corehttp.CommandsOption(cctx),
		corehttp.WebUIOption,
		gatewayOpt,
	}

	addr, err := ma.NewMultiaddr("/ip4/0.0.0.0/tcp/5001")
	if err != nil {
		return err
	}
	list, err := manet.Listen(addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		list.Close()
	}()
	// The address might have changed, if the port was 0; use list.Multiaddr
	// to fetch the final one.

	// Avoid corehttp.ListenAndServe, since it doesn't provide the final
	// address, and always prints to stdout.
	go corehttp.Serve(node, manet.NetListener(list), opts...)

	i.Node = node
	i.CoreAPI = coreAPI
	i.DataDir = d.Datadir

	return nil
}

func (i *IPFSHandle) Stop() error {
	i.cancel()
	return i.Node.Close()
}

// URIprefix returns the URI prefix which identifies the protocol
func (i *IPFSHandle) URIprefix() string {
	return "ipfs://"
}

// PublishBytes publishes a file containing msg to ipfs
func PublishBytes(ctx context.Context, msg []byte, fileDir string, node *ipfscore.IpfsNode) (string, error) {
	filePath := fmt.Sprintf("%s/%x", fileDir, crypto.HashRaw(msg))
	log.Infof("publishing file: %s", filePath)
	err := ioutil.WriteFile(filePath, msg, 0666)
	if err != nil {
		return "", err
	}
	rootHash, err := addAndPin(ctx, node, filePath)
	if err != nil {
		return "", err
	}
	return rootHash, nil
}

// Publish publishes a message to ipfs
func (i *IPFSHandle) Publish(ctx context.Context, msg []byte) (string, error) {
	// if sent a message instead of a file
	return PublishBytes(ctx, msg, i.DataDir, i.Node)
}

func addAndPin(ctx context.Context, n *ipfscore.IpfsNode, root string) (rootHash string, err error) {
	defer n.Blockstore.PinLock().Unlock()
	stat, err := os.Lstat(root)
	if err != nil {
		return "", err
	}

	f, err := files.NewSerialFile(root, false, stat)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fileAdder, err := coreunix.NewAdder(ctx, n.Pinning, n.Blockstore, n.DAG)
	if err != nil {
		return "", err
	}

	node, err := fileAdder.AddAllAndPin(f)
	if err != nil {
		return "", err
	}
	return node.Cid().String(), nil
}

func (i *IPFSHandle) Pin(ctx context.Context, path string) error {
	// path = strings.ReplaceAll(path, "/ipld/", "/ipfs/")

	p := corepath.New(path)
	rp, err := i.CoreAPI.ResolvePath(ctx, p)
	if err != nil {
		return err
	}
	return i.CoreAPI.Pin().Add(ctx, rp, options.Pin.Recursive(true))
}

func (i *IPFSHandle) Unpin(ctx context.Context, path string) error {
	p := corepath.New(path)
	rp, err := i.CoreAPI.ResolvePath(ctx, p)
	if err != nil {
		return err
	}
	return i.CoreAPI.Pin().Rm(ctx, rp, options.Pin.RmRecursive(true))
}

func (i *IPFSHandle) Stats(ctx context.Context) (string, error) {
	response := ""
	peers, err := i.CoreAPI.Swarm().Peers(ctx)
	if err != nil {
		return response, err
	}
	addresses, err := i.CoreAPI.Swarm().KnownAddrs(ctx)
	if err != nil {
		return response, err
	}
	pins, err := i.CoreAPI.Pin().Ls(ctx)
	if err != nil {
		return response, err
	}
	return fmt.Sprintf("peers:%d addresses:%d pins:%d", len(peers), len(addresses), len(pins)), nil
}

func (i *IPFSHandle) ListPins(ctx context.Context) (map[string]string, error) {
	pins, err := i.CoreAPI.Pin().Ls(ctx, options.Pin.Ls.All())
	if err != nil {
		return nil, err
	}
	pinMap := make(map[string]string)
	for p := range pins {
		pinMap[p.Path().String()] = p.Type()
	}
	return pinMap, nil
}

func (i *IPFSHandle) Retrieve(ctx context.Context, path string) ([]byte, error) {
	path = strings.TrimPrefix(path, "ipfs://")
	pth := corepath.New(path)

	node, err := i.CoreAPI.Unixfs().Get(ctx, pth)
	if err != nil {
		return nil, err
	}
	defer node.Close()
	if s, err := node.Size(); s > int64(MaxFileSizeBytes) || err != nil {
		return nil, fmt.Errorf("file too big or size cannot be obtained")
	}
	r, ok := node.(files.File)
	if !ok {
		return nil, errors.New("received incorrect type from Unixfs().Get()")
	}
	return ioutil.ReadAll(r)
}
