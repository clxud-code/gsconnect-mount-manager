package gsconnect

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSConnect/gsconnect-mount-manager/internal/model"
	"github.com/godbus/dbus/v5"
)

const (
	name            = "org.gnome.Shell.Extensions.GSConnect"
	root            = "/org/gnome/Shell/Extensions/GSConnect"
	deviceInterface = "org.gnome.Shell.Extensions.GSConnect.Device"
)

type Client struct{ conn *dbus.Conn }

func Connect() (*Client, error) {
	c, e := dbus.ConnectSessionBus()
	if e != nil {
		return nil, e
	}
	return &Client{c}, nil
}
func (c *Client) Close() { c.conn.Close() }
func (c *Client) Available() error {
	return c.conn.BusObject().Call("org.freedesktop.DBus.Peer.Ping", 0).Err
}
func (c *Client) Devices(ctx context.Context) ([]model.Device, error) {
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	call := c.conn.Object(name, dbus.ObjectPath(root)).CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0)
	if call.Err != nil {
		return nil, call.Err
	}
	if err := call.Store(&objects); err != nil {
		return nil, err
	}
	var out []model.Device
	for path, ifaces := range objects {
		p, ok := ifaces[deviceInterface]
		if !ok {
			continue
		}
		d := model.Device{ID: valueString(p, "Id"), Name: valueString(p, "Name"), Type: valueString(p, "Type"), Connected: valueBool(p, "Connected"), Paired: valueBool(p, "Paired")}
		if d.ID == "" {
			d.ID = strings.TrimPrefix(string(path), root+"/Device/")
		}
		out = append(out, d)
	}
	return out, nil
}
func valueString(p map[string]dbus.Variant, k string) string {
	if v, ok := p[k]; ok {
		if x, ok := v.Value().(string); ok {
			return x
		}
	}
	return ""
}
func valueBool(p map[string]dbus.Variant, k string) bool {
	if v, ok := p[k]; ok {
		if x, ok := v.Value().(bool); ok {
			return x
		}
	}
	return false
}
func (c *Client) Action(ctx context.Context, id, action string) error {
	if id == "" {
		return fmt.Errorf("empty device id")
	}
	path := dbus.ObjectPath(root + "/Device/" + id)
	return c.conn.Object(name, path).CallWithContext(ctx, "org.gtk.Actions.Activate", 0, action, []dbus.Variant{}, map[string]dbus.Variant{}).Err
}
func (c *Client) Subscribe() (<-chan *dbus.Signal, error) {
	if err := c.conn.AddMatchSignal(dbus.WithMatchSender(name)); err != nil {
		return nil, err
	}
	ch := make(chan *dbus.Signal, 32)
	c.conn.Signal(ch)
	return ch, nil
}
