package model

import "time"

type State string

const (
	Disconnected State = "DISCONNECTED"
	Discovered   State = "DISCOVERED"
	Connecting   State = "CONNECTING"
	Mounted      State = "MOUNTED"
	Degraded     State = "DEGRADED"
	Reconnecting State = "RECONNECTING"
)

type Device struct {
	ID, Name, Type    string
	Connected, Paired bool
}
type Status struct {
	Device                          Device
	State                           State
	MountURI, Link, Root, Bookmark, LastError string
	Attempts                        int
	NextRetry                       time.Time
}
