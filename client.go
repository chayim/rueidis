package rueidis

import (
	"context"
	"time"

	"github.com/rueian/rueidis/internal/cmds"
	"github.com/rueian/rueidis/internal/proto"
	"github.com/rueian/rueidis/om"
)

type SingleClientOption struct {
	Address    string
	ConnOption ConnOption
}

type SingleClient struct {
	cmd  *cmds.Builder
	conn conn
}

func newSingleClient(opt SingleClientOption, connFn connFn) (*SingleClient, error) {
	client := &SingleClient{cmd: cmds.NewBuilder(cmds.NoSlot), conn: connFn(opt.Address, opt.ConnOption)}

	if err := client.conn.Dial(); err != nil {
		return nil, err
	}

	opt.ConnOption.PubSubHandlers.installHook(client.cmd, func() conn { return client.conn })

	return client, nil
}

func (c *SingleClient) B() *cmds.Builder {
	return c.cmd
}

func (c *SingleClient) Do(ctx context.Context, cmd cmds.Completed) (resp proto.Result) {
	resp = c.conn.Do(cmd)
	c.cmd.Put(cmd.CommandSlice())
	return resp
}

func (c *SingleClient) DoCache(ctx context.Context, cmd cmds.Cacheable, ttl time.Duration) (resp proto.Result) {
	resp = c.conn.DoCache(cmd, ttl)
	c.cmd.Put(cmd.CommandSlice())
	return resp
}

func (c *SingleClient) Dedicated(fn func(DedicatedClient) error) (err error) {
	wire := c.conn.Acquire()
	err = fn(&dedicatedSingleClient{cmd: c.cmd, wire: wire})
	c.conn.Store(wire)
	return err
}

func (c *SingleClient) NewLuaScript(body string) *Lua {
	return newLuaScript(body, c.eval, c.evalSha)
}

func (c *SingleClient) NewLuaScriptReadOnly(body string) *Lua {
	return newLuaScript(body, c.evalRo, c.evalShaRo)
}

func (c *SingleClient) eval(ctx context.Context, body string, keys, args []string) proto.Result {
	return c.Do(ctx, c.cmd.Eval().Script(body).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build())
}

func (c *SingleClient) evalSha(ctx context.Context, sha string, keys, args []string) proto.Result {
	return c.Do(ctx, c.cmd.Evalsha().Sha1(sha).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build())
}

func (c *SingleClient) evalRo(ctx context.Context, body string, keys, args []string) proto.Result {
	return c.Do(ctx, c.cmd.EvalRo().Script(body).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build())
}

func (c *SingleClient) evalShaRo(ctx context.Context, sha string, keys, args []string) proto.Result {
	return c.Do(ctx, c.cmd.EvalshaRo().Sha1(sha).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build())
}

func (c *SingleClient) NewHashRepository(prefix string, schema interface{}) *om.HashRepository {
	return om.NewHashRepository(prefix, schema, &hashObjectSingleClientAdapter{c: c}, func(script string) om.ExecFn {
		return c.NewLuaScript(script).Exec
	})
}

func (c *SingleClient) Close() {
	c.conn.Close()
}

type dedicatedSingleClient struct {
	cmd  *cmds.Builder
	wire wire
}

func (c *dedicatedSingleClient) B() *cmds.Builder {
	return c.cmd
}

func (c *dedicatedSingleClient) Do(ctx context.Context, cmd cmds.Completed) (resp proto.Result) {
	resp = c.wire.Do(cmd)
	c.cmd.Put(cmd.CommandSlice())
	return resp
}

func (c *dedicatedSingleClient) DoMulti(ctx context.Context, multi ...cmds.Completed) (resp []proto.Result) {
	if len(multi) == 0 {
		return nil
	}
	resp = c.wire.DoMulti(multi...)
	for _, cmd := range multi {
		c.cmd.Put(cmd.CommandSlice())
	}
	return resp
}

type hashObjectSingleClientAdapter struct {
	c *SingleClient
}

func (h *hashObjectSingleClientAdapter) Save(ctx context.Context, key string, fields map[string]string) error {
	cmd := h.c.cmd.Hset().Key(key).FieldValue()
	for f, v := range fields {
		cmd = cmd.FieldValue(f, v)
	}
	return h.c.Do(ctx, cmd.Build()).Error()
}

func (h *hashObjectSingleClientAdapter) Fetch(ctx context.Context, key string) (map[string]proto.Message, error) {
	return h.c.Do(ctx, h.c.cmd.Hgetall().Key(key).Build()).ToMap()
}

func (h *hashObjectSingleClientAdapter) FetchCache(ctx context.Context, key string, ttl time.Duration) (map[string]proto.Message, error) {
	return h.c.DoCache(ctx, h.c.cmd.Hgetall().Key(key).Cache(), ttl).ToMap()
}

func (h *hashObjectSingleClientAdapter) Remove(ctx context.Context, key string) error {
	return h.c.Do(ctx, h.c.cmd.Del().Key(key).Build()).Error()
}
