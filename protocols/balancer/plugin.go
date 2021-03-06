package balancer

import (
	"fmt"
	"sort"

	"github.com/antongulenko/RTP/protocols"
	"github.com/antongulenko/golib"
)

const (
	num_backup_servers    = 1
	backup_session_weight = 0.1
)

type FaultDetectorFactory func(endpoint string) (protocols.FaultDetector, error)

type BalancingPlugin struct {
	Server         *protocols.PluginServer
	BackendServers BackendServerSlice

	make_detector FaultDetectorFactory
	handler       BalancingPluginHandler
}

type BalancingPluginHandler interface {
	NewClient(detector protocols.FaultDetector) (protocols.CircuitBreaker, error)
	Protocol() protocols.Protocol

	// Create and fully initialize new session. The param data is passed from
	// plugin to plugin, enabling one plugin to modify the input data for the next plugin.
	// Modify param if necessary and copy values from it. Do not store it.
	NewSession(balancingSession *BalancingSession, param protocols.SessionParameter) (BalancingSessionHandler, error)
}

type BalancingSession struct {
	Client         string
	Plugin         *BalancingPlugin
	SendingSession protocols.PluginSessionHandler
	Handler        BalancingSessionHandler

	PrimaryServer *BackendServer
	BackupServers BackendServerSlice
	failoverError error
}

type BalancingSessionHandler interface {
	StopRemote() error
	RedirectStream(newHost string, newPort int) error
	HandleServerFault() (*BackendServer, error)
}

func NewBalancingPlugin(handler BalancingPluginHandler, make_detector FaultDetectorFactory) *BalancingPlugin {
	return &BalancingPlugin{
		handler:        handler,
		BackendServers: make(BackendServerSlice, 0, 10),
		make_detector:  make_detector,
	}
}

func (plugin *BalancingPlugin) AddBackendServer(addr string, callback protocols.FaultDetectorCallback) error {
	plugin.assertStarted()
	serverAddr, err := plugin.Server.Protocol().Transport().Resolve(addr)
	if err != nil {
		return fmt.Errorf("Error resolving backend server: %v", err)
	}
	detector, err := plugin.make_detector(addr)
	if err != nil {
		return fmt.Errorf("Error making detector: %v", err)
	}
	client, err := plugin.handler.NewClient(detector)
	if err != nil {
		_ = detector.Close()
		return fmt.Errorf("Error creating client: %v", err)
	}
	err = client.SetServer(serverAddr.String())
	if err != nil {
		_ = detector.Close()
		_ = client.Close()
		return fmt.Errorf("Error configuring client: %v", err)
	}
	server := &BackendServer{
		Addr:     serverAddr,
		Client:   client,
		Sessions: make(map[*BalancingSession]bool),
		Plugin:   plugin,
	}
	plugin.BackendServers = append(plugin.BackendServers, server)
	sort.Sort(plugin.BackendServers)
	if callback != nil {
		client.AddCallback(callback, client)
	}
	client.AddCallback(plugin.serverStateChanged, server)
	return nil
}

func (plugin *BalancingPlugin) Start(server *protocols.PluginServer) {
	plugin.Server = server
}

func (plugin *BalancingPlugin) NewSession(param protocols.SessionParameter) (protocols.PluginSessionHandler, error) {
	clientAddr := param.Client()
	server, backups := plugin.BackendServers.pickServer(clientAddr)
	if server == nil {
		return nil, fmt.Errorf("No %s server available to handle your request", plugin.handler.Protocol().Name())
	}
	session := &BalancingSession{
		PrimaryServer: server,
		Plugin:        plugin,
		Client:        clientAddr,
		BackupServers: backups,
	}
	var err error
	session.Handler, err = plugin.handler.NewSession(session, param)
	if err != nil {
		return nil, fmt.Errorf("Failed to create %s session: %s", plugin.handler.Protocol().Name(), err)
	}
	server.registerSession(session)
	return session, nil
}

func (plugin *BalancingPlugin) Stop() error {
	var errors golib.MultiError
	for _, server := range plugin.BackendServers {
		if err := server.Client.Close(); err != nil {
			errors = append(errors, fmt.Errorf("Error closing connection to %s: %v", server.Client, err))
		}
	}
	return errors.NilOrError()
}

func (plugin *BalancingPlugin) serverStateChanged(key interface{}) {
	server, ok := key.(*BackendServer)
	if !ok {
		plugin.assertStarted()
		plugin.Server.LogError(fmt.Errorf("Could not handle server fault: Failed to convert %v (%T) to *BackendServer", key, key))
		return
	}
	server.handleStateChanged()
}

func (plugin *BalancingPlugin) assertStarted() {
	// TODO HACK for debugging
	if plugin.Server == nil {
		panic("BalancingPlugin was not yet started")
	}
}

func (session *BalancingSession) StopContainingSession() error {
	session.Plugin.assertStarted()
	return session.Plugin.Server.StopSession(session.Client)
}

func (session *BalancingSession) LogServerError(err error) {
	session.Plugin.assertStarted()
	session.Plugin.Server.LogError(err)
}

func (session *BalancingSession) Start(sendingSession protocols.PluginSessionHandler) {
	session.SendingSession = sendingSession
	// Nothing else to do. BalancingSession.NewSession() fully starts the session.
}

func (session *BalancingSession) Tasks() []golib.Task {
	return nil // No tasks
}

func (session *BalancingSession) Cleanup() error {
	session.PrimaryServer.unregisterSession(session)
	if session.failoverError == nil {
		return session.Handler.StopRemote()
	} else {
		// Failover for the session failed previously. Don't try to close it anymore.
		return session.failoverError
	}
}

func (session *BalancingSession) String() string {
	return session.PrimaryServer.String()
}
