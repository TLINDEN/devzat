package devzatapi

import (
	"context"
	"devzat/plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type Message struct {
	Room,
	From,
	Data string
}

type CmdInvocation struct {
	Room,
	From,
	Args string
}

type Session struct {
	conn         *grpc.ClientConn
	pluginClient plugin.PluginClient

	ErrorChan chan error
}

// NewSession connects to the Devzat server and creates a session. The address should be in the form of "host:port".
func NewSession(address string, token string) (*Session, error) {
	conn, err := grpc.Dial(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStreamInterceptor(
			func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
				ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
				return streamer(ctx, desc, cc, method, opts...)
			},
		),
		grpc.WithUnaryInterceptor(
			func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
				ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
				return invoker(ctx, method, req, reply, cc, opts...)
			},
		),
	)
	if err != nil {
		return nil, err
	}
	return &Session{conn: conn, pluginClient: plugin.NewPluginClient(conn), ErrorChan: make(chan error)}, nil
}

// Close closes the session.
func (s *Session) Close() error {
	return s.conn.Close()
}

// RegisterListener allows for message monitoring and intercepting/editing.
// Set middleware to true if you want to intercept and edit messages.
// Set once to true if you want to unregister the listener after the first message is received.
// Set regex to a valid regex string if you want to only receive messages that match the regex.
// The messageChan will receive messages that match the regex.
// The middlewareResponseChan is used to send back the edited message. You must send a response if middleware is true
// even if you don't edit the message.
// Make sure to always read from ErrorChan when sending a response and when reading messages.
func (s *Session) RegisterListener(middleware, once bool, regex string) (messageChan chan Message, middlewareResponseChan chan string, err error) {
	client, err := s.pluginClient.RegisterListener(context.Background())
	if err != nil {
		return
	}
	pointerRegex := &regex
	if regex == "" {
		pointerRegex = nil
	}
	err = client.Send(&plugin.ListenerClientData{Data: &plugin.ListenerClientData_Listener{Listener: &plugin.Listener{
		Middleware: &middleware,
		Once:       &once,
		Regex:      pointerRegex,
	}}})
	if err != nil {
		return
	}

	messageChan = make(chan Message)
	var e *plugin.Event
	go func() {
		for {
			e, err = client.Recv()
			if err != nil {
				messageChan <- Message{}
				s.ErrorChan <- err
				continue
			}
			messageChan <- Message{Room: e.Room, From: e.From, Data: e.Msg}
			s.ErrorChan <- nil
		}
	}()

	if !middleware {
		return
	}

	middlewareResponseChan = make(chan string)
	go func() {
		for {
			response := <-middlewareResponseChan
			err := client.Send(&plugin.ListenerClientData{Data: &plugin.ListenerClientData_Response{Response: &plugin.MiddlewareResponse{Msg: &response}}})
			if err != nil {
				s.ErrorChan <- err
				continue
			}
			s.ErrorChan <- nil
		}
	}()

	return
}

func (s *Session) SendMessage(room, from, msg, dmTo string) error {
	fromPtr := &from
	if from == "" {
		fromPtr = nil
	}
	dmToPtr := &dmTo
	if dmTo == "" {
		dmToPtr = nil
	}
	_, err := s.pluginClient.SendMessage(context.Background(), &plugin.Message{
		Room:        room,
		From:        fromPtr,
		Msg:         msg,
		EphemeralTo: dmToPtr,
	})
	return err
}

// read CmdInvocation.Error each time
func (s *Session) RegisterCmd(name, argsInfo, info string) (chan CmdInvocation, error) {
	client, err := s.pluginClient.RegisterCmd(context.Background(), &plugin.CmdDef{
		Name:     name,
		ArgsInfo: argsInfo,
		Info:     info,
	})
	if err != nil {
		return nil, err
	}
	cmdInvocChan := make(chan CmdInvocation)
	go func() {
		for {
			invoc, err := client.Recv()
			if err != nil {
				cmdInvocChan <- CmdInvocation{}
				s.ErrorChan <- err
				continue
			}
			cmdInvocChan <- CmdInvocation{Room: invoc.Room, From: invoc.From, Args: invoc.Args}
			s.ErrorChan <- nil
		}
	}()
	return cmdInvocChan, nil
}