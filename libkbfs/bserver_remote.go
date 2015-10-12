package libkbfs

import (
	"encoding/hex"
	"time"

	"github.com/keybase/client/go/logger"
	keybase1 "github.com/keybase/client/go/protocol"
	"golang.org/x/net/context"
)

// BlockServerRemote implements the BlockServer interface and
// represents a remote KBFS block server.
type BlockServerRemote struct {
	config        Config
	clientFactory ClientFactory
	log           logger.Logger
	blkSrvAddr    string
}

// Test that BlockServerRemote fully implements the BlockServer interface.
var _ BlockServer = (*BlockServerRemote)(nil)

// NewBlockServerRemote constructs a new BlockServerRemote for the
// given address.
func NewBlockServerRemote(ctx context.Context, config Config, blkSrvAddr string) *BlockServerRemote {
	bs := &BlockServerRemote{
		config:     config,
		log:        config.MakeLogger(""),
		blkSrvAddr: blkSrvAddr,
	}
	bs.log.CDebugf(ctx, "BlockServerRemote new instance "+
		"server addr %s\n", blkSrvAddr)
	conn := NewConnection(ctx, config, blkSrvAddr, bs, bServerErrorUnwrapper{})
	bs.clientFactory = ConnectionClientFactory{conn}
	return bs
}

// For testing.
func newBlockServerRemoteWithClient(ctx context.Context, config Config,
	testClient keybase1.GenericClient) *BlockServerRemote {
	bs := &BlockServerRemote{
		config:        config,
		clientFactory: CancelableClientFactory{testClient},
		log:           config.MakeLogger(""),
	}
	return bs
}

// RemoteAddress returns the remote bserver this client is talking to
func (b *BlockServerRemote) RemoteAddress() string {
	return b.blkSrvAddr
}

// OnConnect implements the ConnectionHandler interface.
func (b *BlockServerRemote) OnConnect(ctx context.Context,
	conn *Connection, client keybase1.GenericClient) error {
	token, err := b.config.KBPKI().GetCurrentToken(ctx)
	if err != nil {
		b.log.CWarningf(ctx, "BlockServerRemote: error getting session %q", err)
		return err
	}

	uid, err := b.config.KBPKI().GetCurrentUID(ctx)
	if err != nil {
		return err
	}

	arg := keybase1.EstablishSessionArg{
		User: uid,
		Sid:  token,
	}

	b.log.CDebugf(ctx, "BlockServerRemote.OnConnect establish session for "+
		"uid %s\n", uid.String())
	// Using GetClient() here would cause problematic recursion.
	c := keybase1.BlockClient{Cli: client}
	return runUnlessCanceled(ctx, func() error {
		return c.EstablishSession(arg)
	})
}

// OnConnectError implements the ConnectionHandler interface.
func (b *BlockServerRemote) OnConnectError(err error, wait time.Duration) {
	b.log.Warning("BlockServerRemote: connection error: %v; retrying in %s",
		err, wait)
	// TODO: it might make sense to show something to the user if this is
	// due to authentication, for example.
}

// Helper to return a metadata client.
func (b *BlockServerRemote) client(ctx context.Context) keybase1.BlockClient {
	return keybase1.BlockClient{Cli: b.clientFactory.GetClient(ctx)}
}

// OnDisconnected implements the ConnectionHandler interface.
func (b *BlockServerRemote) OnDisconnected() {
	b.log.Warning("BlockServerRemote is disconnected")
}

// ShouldThrottle implements the ConnectionHandler interface.
func (b *BlockServerRemote) ShouldThrottle(err error) bool {
	if err == nil {
		return false
	}
	_, shouldThrottle := err.(BServerErrorThrottle)
	return shouldThrottle
}

// Get implements the BlockServer interface for BlockServerRemote.
func (b *BlockServerRemote) Get(ctx context.Context, id BlockID,
	context BlockContext) ([]byte, BlockCryptKeyServerHalf, error) {
	var err error
	size := -1
	defer func() {
		b.log.CDebugf(ctx, "BlockServerRemote.Get id=%s uid=%s sz=%d err=%v",
			id, context.GetWriter(), size, err)
	}()

	bid := keybase1.BlockIdCombo{
		BlockHash: id.String(),
		ChargedTo: context.GetWriter(),
	}

	res, err := b.client(ctx).GetBlock(bid)
	if err != nil {
		return nil, BlockCryptKeyServerHalf{}, err
	}

	size = len(res.Buf)
	bk := BlockCryptKeyServerHalf{}
	var kbuf []byte
	if kbuf, err = hex.DecodeString(res.BlockKey); err != nil {
		return nil, BlockCryptKeyServerHalf{}, err
	}
	copy(bk.ServerHalf[:], kbuf)
	return res.Buf, bk, nil
}

// Put implements the BlockServer interface for BlockServerRemote.
// TODO: store the server-half of the block key
func (b *BlockServerRemote) Put(ctx context.Context, id BlockID, tlfID TlfID,
	context BlockContext, buf []byte,
	serverHalf BlockCryptKeyServerHalf) error {
	var err error
	size := len(buf)
	defer func() {
		b.log.CDebugf(ctx, "BlockServerRemote.Put id=%s uid=%s sz=%d err=%v",
			id, context.GetWriter(), size, err)
	}()

	arg := keybase1.PutBlockArg{
		Bid: keybase1.BlockIdCombo{
			ChargedTo: context.GetWriter(),
			BlockHash: id.String(),
		},
		BlockKey: serverHalf.String(),
		Folder:   tlfID.String(),
		Buf:      buf,
	}

	err = b.client(ctx).PutBlock(arg)
	return err
}

// AddBlockReference implements the BlockServer interface for BlockServerRemote
func (b *BlockServerRemote) AddBlockReference(ctx context.Context, id BlockID,
	tlfID TlfID, context BlockContext) error {
	var err error
	defer func() {
		b.log.CDebugf(ctx, "BlockServerRemote.AddBlockReference id=%s uid=%s err=%v",
			id, context.GetWriter(), err)
	}()

	arg := keybase1.IncBlockReferenceArg{
		Bid: keybase1.BlockIdCombo{
			ChargedTo: context.GetCreator(),
			BlockHash: id.String(),
		},
		Folder:    tlfID.String(),
		ChargedTo: context.GetWriter(), //the actual writer to decrement quota from
	}
	nonce := context.GetRefNonce()
	copy(arg.Nonce[:], nonce[:])

	err = b.client(ctx).IncBlockReference(arg)
	return err
}

// RemoveBlockReference implements the BlockServer interface for
// BlockServerRemote
func (b *BlockServerRemote) RemoveBlockReference(ctx context.Context, id BlockID,
	tlfID TlfID, context BlockContext) error {
	var err error
	defer func() {
		b.log.CDebugf(ctx, "BlockServerRemote.RemoveBlockReference id=%s uid=%s err=%v",
			id, context.GetWriter(), err)
	}()

	arg := keybase1.DecBlockReferenceArg{
		Bid: keybase1.BlockIdCombo{
			ChargedTo: context.GetCreator(),
			BlockHash: id.String(),
		},
		Folder:    tlfID.String(),
		ChargedTo: context.GetWriter(), //the actual writer to decrement quota from
	}
	nonce := context.GetRefNonce()
	copy(arg.Nonce[:], nonce[:])

	err = b.client(ctx).DecBlockReference(arg)
	return err
}

// Shutdown implements the BlockServer interface for BlockServerRemote.
func (b *BlockServerRemote) Shutdown() {
	b.clientFactory.Shutdown()
}
