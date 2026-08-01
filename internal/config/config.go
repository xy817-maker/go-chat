package config

import (
	"github.com/BurntSushi/toml"
	"log"
	"time"
)

type MainConfig struct {
	AppName string `toml:"appName"`
	Host    string `toml:"host"`
	Port    int    `toml:"port"`
}

type MysqlConfig struct {
	Host         string `toml:"host"`
	Port         int    `toml:"port"`
	User         string `toml:"user"`
	Password     string `toml:"password"`
	DatabaseName string `toml:"databaseName"`
}

type RedisConfig struct {
	Host     string `toml:"host"`
	Port     int    `toml:"port"`
	Password string `toml:"password"`
	Db       int    `toml:"db"`
}

type AuthCodeConfig struct {
	AccessKeyID     string `toml:"accessKeyID"`
	AccessKeySecret string `toml:"accessKeySecret"`
	SignName        string `toml:"signName"`
	TemplateCode    string `toml:"templateCode"`
}

type LogConfig struct {
	LogPath string `toml:"logPath"`
}

type MessageQueueConfig struct {
	MessageMode string        `toml:"messageMode"` // channel or rocketmq
	HostPort    string        `toml:"hostPort"`    // RocketMQ NameServer 地址
	GroupName   string        `toml:"groupName"`
	LoginTopic  string        `toml:"loginTopic"`
	LogoutTopic string        `toml:"logoutTopic"`
	ChatTopic   string        `toml:"chatTopic"`
	Partition   int           `toml:"partition"` // 兼容字段，RocketMQ 不使用
	Timeout     time.Duration `toml:"timeout"`
}

type StaticSrcConfig struct {
	StaticAvatarPath string `toml:"staticAvatarPath"`
	StaticFilePath   string `toml:"staticFilePath"`
}

type RateLimitConfig struct {
	Rps   float64 `toml:"rps"`   // 每秒令牌数（按 IP）
	Burst int     `toml:"burst"` // 令牌桶容量
}

type Config struct {
	MainConfig         `toml:"mainConfig"`
	MysqlConfig        `toml:"mysqlConfig"`
	RedisConfig        `toml:"redisConfig"`
	AuthCodeConfig     `toml:"authCodeConfig"`
	LogConfig          `toml:"logConfig"`
	MessageQueueConfig `toml:"messageQueueConfig"`
	StaticSrcConfig    `toml:"staticSrcConfig"`
	RateLimitConfig    `toml:"rateLimitConfig"`
}

var config *Config

func LoadConfig() error {
	// 本地部署
	// if _, err := toml.DecodeFile("F:\\go\\kama-chat-server\\configs\\config_local.toml", config); err != nil {
	// 	log.Fatal(err.Error())
	// 	return err
	// }
	// 本地部署
	if _, err := toml.DecodeFile("configs/config.toml", config); err != nil {
		log.Fatal(err.Error())
		return err
	}
	return nil
}

func GetConfig() *Config {
	if config == nil {
		config = new(Config)
		_ = LoadConfig()
	}
	return config
}
