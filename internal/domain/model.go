package domain

type Parameter struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
}

type Feature struct {
	APIVersion        string               `json:"apiVersion"`
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Version           string               `json:"version"`
	Description       string               `json:"description,omitempty"`
	Entrypoint        string               `json:"entrypoint"`
	SupportedOS       []string             `json:"supportedOS,omitempty"`
	RequireRoot       bool                 `json:"requireRoot,omitempty"`
	RemoteOnly        bool                 `json:"remoteOnly,omitempty"`
	RemoteArgsExample string               `json:"remoteArgsExample,omitempty"`
	TimeoutSeconds    int                  `json:"timeoutSeconds,omitempty"`
	DependsOn         []string             `json:"dependsOn,omitempty"`
	Parameters        map[string]Parameter `json:"parameters,omitempty"`
	Directory         string               `json:"-"`
}

type FeatureSelection struct {
	ID         string         `json:"id"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type Profile struct {
	APIVersion    string             `json:"apiVersion"`
	Name          string             `json:"name"`
	Description   string             `json:"description,omitempty"`
	Features      []FeatureSelection `json:"features"`
	RemoteServers []RemoteServer     `json:"remoteServers,omitempty"`
	System        string             `json:"-"`
	Path          string             `json:"-"`
}

type RemoteServer struct {
	Name     string `json:"name,omitempty"`
	IP       string `json:"ip"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type ResolvedFeature struct {
	Feature    Feature
	Parameters map[string]any
}

type Status string

const (
	StatusPlanned Status = "planned"
	StatusSkipped Status = "skipped"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

type Result struct {
	FeatureID string
	Status    Status
	Message   string
}
