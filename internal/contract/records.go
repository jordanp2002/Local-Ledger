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

type Page struct {
	Limit    int64 `json:"limit"`
	Offset   int64 `json:"offset"`
	Returned int64 `json:"returned"`
	Total    int64 `json:"total"`
	HasMore  bool  `json:"has_more"`
}

type MonthlySummaryCategory struct {
	CategoryID int64  `json:"category_id"`
	Category   string `json:"category"`
	Budget     string `json:"budget"`
	Spending   string `json:"spending"`
	Remaining  string `json:"remaining"`
}

type CategorySummary struct {
	CategoryID       int64  `json:"category_id"`
	Category         string `json:"category"`
	Month            string `json:"month"`
	Budget           string `json:"budget"`
	TotalSpending    string `json:"total_spending"`
	Remaining        string `json:"remaining"`
	TransactionCount int64  `json:"transaction_count"`
}

// ComparisonMonth contains one side of a two-month comparison.
type ComparisonMonth struct {
	Month         string `json:"month"`
	TotalBudget   string `json:"total_budget"`
	TotalSpending string `json:"total_spending"`
	Remaining     string `json:"remaining"`
}

// ComparisonChange contains the later-month minus earlier-month totals.
type ComparisonChange struct {
	TotalBudget   string `json:"total_budget"`
	TotalSpending string `json:"total_spending"`
	Remaining     string `json:"remaining"`
}

// ComparisonCategory contains one category's values in both months and their changes.
type ComparisonCategory struct {
	CategoryID     int64  `json:"category_id"`
	Category       string `json:"category"`
	FromBudget     string `json:"from_budget"`
	ToBudget       string `json:"to_budget"`
	BudgetChange   string `json:"budget_change"`
	FromSpending   string `json:"from_spending"`
	ToSpending     string `json:"to_spending"`
	SpendingChange string `json:"spending_change"`
}
