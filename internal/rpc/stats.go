package rpc

import "encoding/json"

const (
	MethodChatStats  = "chat.stats"
	MethodChatBoosts = "chat.boosts"
)

type ChatStatsParams struct {
	Chat  string `json:"chat"`
	Graph string `json:"graph,omitempty"`
}
type StatsValue struct {
	Current  float64 `json:"current"`
	Previous float64 `json:"previous"`
}
type StatsPeriod struct {
	MinDate string `json:"min_date"`
	MaxDate string `json:"max_date"`
}
type StatsGraph struct {
	Name  string `json:"name"`
	State string `json:"state"`
}
type StatsPost struct {
	Kind      string `json:"kind"`
	ID        int    `json:"id"`
	Views     int    `json:"views"`
	Forwards  int    `json:"forwards"`
	Reactions int    `json:"reactions"`
}
type StatsPerson struct {
	UserID      int64  `json:"user_id"`
	Name        string `json:"name" untrusted:"true"`
	Messages    int    `json:"messages,omitempty"`
	AvgChars    int    `json:"avg_chars,omitempty"`
	Deleted     int    `json:"deleted,omitempty"`
	Kicked      int    `json:"kicked,omitempty"`
	Banned      int    `json:"banned,omitempty"`
	Invitations int    `json:"invitations,omitempty"`
}
type ChatStats struct {
	ChatID                      int64           `json:"chat_id"`
	Kind                        string          `json:"kind,omitempty"`
	Period                      *StatsPeriod    `json:"period,omitempty"`
	Followers                   *StatsValue     `json:"followers,omitempty"`
	ViewsPerPost                *StatsValue     `json:"views_per_post,omitempty"`
	SharesPerPost               *StatsValue     `json:"shares_per_post,omitempty"`
	ReactionsPerPost            *StatsValue     `json:"reactions_per_post,omitempty"`
	ViewsPerStory               *StatsValue     `json:"views_per_story,omitempty"`
	SharesPerStory              *StatsValue     `json:"shares_per_story,omitempty"`
	ReactionsPerStory           *StatsValue     `json:"reactions_per_story,omitempty"`
	EnabledNotificationsPercent *float64        `json:"enabled_notifications_percent,omitempty"`
	Members                     *StatsValue     `json:"members,omitempty"`
	Messages                    *StatsValue     `json:"messages,omitempty"`
	Viewers                     *StatsValue     `json:"viewers,omitempty"`
	Posters                     *StatsValue     `json:"posters,omitempty"`
	RecentPosts                 []StatsPost     `json:"recent_posts,omitempty"`
	TopPosters                  []StatsPerson   `json:"top_posters,omitempty"`
	TopAdmins                   []StatsPerson   `json:"top_admins,omitempty"`
	TopInviters                 []StatsPerson   `json:"top_inviters,omitempty"`
	Graphs                      []StatsGraph    `json:"graphs,omitempty"`
	Name                        string          `json:"name,omitempty"`
	Data                        json.RawMessage `json:"data,omitempty" untrusted:"true"`
}
type ChatBoosts struct {
	ChatID                 int64    `json:"chat_id"`
	Level                  int      `json:"level"`
	CurrentLevelBoosts     int      `json:"current_level_boosts"`
	Boosts                 int      `json:"boosts"`
	GiftBoosts             *int     `json:"gift_boosts,omitempty"`
	NextLevelBoosts        *int     `json:"next_level_boosts,omitempty"`
	PremiumAudiencePercent *float64 `json:"premium_audience_percent,omitempty"`
	BoostURL               string   `json:"boost_url"`
	MyBoost                bool     `json:"my_boost"`
}
