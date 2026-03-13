package streamers

type Streamer struct {
	ID          string `json:"id"`
	Platform    string `json:"platform"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Online      bool   `json:"online"`
	Viewers     int    `json:"viewers"`
	AddedBy     string `json:"addedBy"`
	Status      string `json:"status"`
}

type Submission struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}
