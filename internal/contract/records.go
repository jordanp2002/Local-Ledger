package contract

type Category struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Budget struct {
	ID         int64  `json:"id"`
	Month      string `json:"month"`
	CategoryID int64  `json:"category_id"`
	Category   string `json:"category"`
	Amount     string `json:"amount"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type Transaction struct {
	ID         int64   `json:"id"`
	Amount     string  `json:"amount"`
	Merchant   string  `json:"merchant"`
	Date       string  `json:"date"`
	CategoryID int64   `json:"category_id"`
	Category   string  `json:"category"`
	Note       *string `json:"note"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

type KnownMerchant struct {
	ID             int64  `json:"id"`
	Merchant       string `json:"merchant"`
	CategoryID     int64  `json:"category_id"`
	Category       string `json:"category"`
	CategoryActive bool   `json:"category_active"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}
