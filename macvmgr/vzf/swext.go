package vzf

type SwextProxySettings struct {
	HTTPEnable   bool   `json:"httpEnable"`
	HTTPProxy    string `json:"httpProxy,omitempty"`
	HTTPPort     int    `json:"httpPort,omitempty"`
	HTTPUser     string `json:"httpUser,omitempty"`
	HTTPPassword string `json:"httpPassword,omitempty"`

	HTTPSEnable   bool   `json:"httpsEnable"`
	HTTPSProxy    string `json:"httpsProxy,omitempty"`
	HTTPSPort     int    `json:"httpsPort,omitempty"`
	HTTPSUser     string `json:"httpsUser,omitempty"`
	HTTPSPassword string `json:"httpsPassword,omitempty"`

	SOCKSEnable   bool   `json:"socksEnable"`
	SOCKSProxy    string `json:"socksProxy,omitempty"`
	SOCKSPort     int    `json:"socksPort,omitempty"`
	SOCKSUser     string `json:"socksUser,omitempty"`
	SOCKSPassword string `json:"socksPassword,omitempty"`
}
