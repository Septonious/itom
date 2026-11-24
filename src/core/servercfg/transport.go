package servercfg

// Transport configures physical service resources, not mailbox quota or a
// maximum number of configured accounts. Zero values retain portable defaults.
type Transport struct {
	Layout              string `json:"layout,omitempty"`
	DataShards          int    `json:"data_shards,omitempty"`
	AccountSessions     int    `json:"account_sessions,omitempty"`
	EndpointConnections int    `json:"endpoint_connections,omitempty"`
	LoginParallelism    int    `json:"login_parallelism,omitempty"`
	Participants        int    `json:"participants,omitempty"`
}

func (t Transport) Validate() error {
	if t.Layout != "" && t.Layout != "auto" && t.Layout != "mixed" && t.Layout != "split" {
		return ErrConfig
	}
	if t.DataShards != 0 && t.DataShards != 1 && t.DataShards != 2 && t.DataShards != 4 {
		return ErrConfig
	}
	if t.AccountSessions != 0 && (t.AccountSessions < 2 || t.AccountSessions > 128) {
		return ErrConfig
	}
	if t.EndpointConnections != 0 && (t.EndpointConnections < 2 || t.EndpointConnections > 4096) {
		return ErrConfig
	}
	if t.LoginParallelism < 0 || t.LoginParallelism > 32 || t.Participants < 0 || t.Participants > 2 {
		return ErrConfig
	}
	if t.AccountSessions > 0 && t.Participants > 0 && t.AccountSessions < 2*t.Participants {
		return ErrConfig
	}
	return nil
}
func (t Transport) Effective() Transport {
	if t.Layout == "" {
		t.Layout = "auto"
	}
	if t.DataShards == 0 {
		t.DataShards = 1
	}
	if t.AccountSessions == 0 {
		t.AccountSessions = 10
	}
	if t.EndpointConnections == 0 {
		t.EndpointConnections = 128
	}
	if t.LoginParallelism == 0 {
		t.LoginParallelism = 4
	}
	if t.Participants == 0 {
		t.Participants = 1
	}
	return t
}
func (t Transport) EndpointAccountSessions(server bool) int {
	t = t.Effective()
	if server {
		return max(1, t.AccountSessions/2)
	}
	return max(1, t.AccountSessions/(2*t.Participants))
}
func (c Config) TransportForUsers() Transport {
	t := c.Transport.Effective()
	t.Participants = 0
	for _, u := range c.Users {
		if u.Enabled {
			t.Participants++
		}
	}
	if t.Participants == 0 {
		t.Participants = 1
	}
	return t
}
