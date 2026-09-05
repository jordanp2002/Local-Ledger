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

// BudgetRollover is one explicit one-month budget adjustment.
type BudgetRollover struct {
	ID                  int64   `json:"id"`
	CategoryID          int64   `json:"category_id"`
	Category            string  `json:"category"`
	SourceMonth         string  `json:"source_month"`
	TargetMonth         string  `json:"target_month"`
	Amount              string  `json:"amount"`
	SourceTransactionID *int64  `json:"source_transaction_id"`
	Note                *string `json:"note"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
	Status              string  `json:"status"`
}

type SinkingFundPeriod struct {
	ID             int64   `json:"id"`
	CategoryID     int64   `json:"category_id"`
	Category       string  `json:"category"`
	CategoryActive bool    `json:"category_active"`
	StartMonth     string  `json:"start_month"`
	EndMonth       *string `json:"end_month"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// RolloverOffer is a non-mutating prompt that can follow a transaction write.
type RolloverOffer struct {
	SourceMonth         string `json:"source_month"`
	TargetMonth         string `json:"target_month"`
	CategoryID          int64  `json:"category_id"`
	Category            string `json:"category"`
	SourceTransactionID *int64 `json:"source_transaction_id"`
	BaseBudget          string `json:"base_budget"`
	AvailableBudget     string `json:"available_budget"`
	SpendingAfter       string `json:"spending_after"`
	EligibleRollover    string `json:"eligible_rollover"`
}

type TransactionAllocation struct {
	CategoryID int64  `json:"category_id"`
	Category   string `json:"category"`
	Amount     string `json:"amount"`
}

type Transaction struct {
	ID          int64                   `json:"id"`
	Amount      string                  `json:"amount"`
	Merchant    string                  `json:"merchant"`
	Date        string                  `json:"date"`
	CategoryID  *int64                  `json:"category_id"`
	Category    *string                 `json:"category"`
	Note        *string                 `json:"note"`
	Allocations []TransactionAllocation `json:"allocations"`
	CreatedAt   string                  `json:"created_at"`
	UpdatedAt   string                  `json:"updated_at"`
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

type Account struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	OpeningBalance string  `json:"opening_balance"`
	CurrentBalance string  `json:"current_balance"`
	Active         bool    `json:"active"`
	Note           *string `json:"note"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

type AccountEntry struct {
	ID                int64   `json:"id"`
	AccountID         int64   `json:"account_id"`
	Account           string  `json:"account"`
	Kind              string  `json:"kind"`
	Amount            string  `json:"amount"`
	Delta             string  `json:"delta"`
	Date              string  `json:"date"`
	Note              *string `json:"note"`
	ReversalOfEntryID *int64  `json:"reversal_of_entry_id"`
	TransferID        *int64  `json:"transfer_id"`
	CreatedAt         string  `json:"created_at"`
	BalanceAfter      string  `json:"balance_after"`
}

type AccountTransfer struct {
	ID                   int64   `json:"id"`
	SourceAccountID      int64   `json:"source_account_id"`
	SourceAccount        string  `json:"source_account"`
	DestinationAccountID int64   `json:"destination_account_id"`
	DestinationAccount   string  `json:"destination_account"`
	Amount               string  `json:"amount"`
	Date                 string  `json:"date"`
	Note                 *string `json:"note"`
	ReversalOfTransferID *int64  `json:"reversal_of_transfer_id"`
	Status               string  `json:"status"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

type Page struct {
	Limit    int64 `json:"limit"`
	Offset   int64 `json:"offset"`
	Returned int64 `json:"returned"`
	Total    int64 `json:"total"`
	HasMore  bool  `json:"has_more"`
}

type MonthlySummaryCategory struct {
	CategoryID         int64   `json:"category_id"`
	Category           string  `json:"category"`
	BaseBudget         string  `json:"base_budget"`
	SinkingFund        bool    `json:"sinking_fund"`
	SinkingFundOpening string  `json:"sinking_fund_opening_balance"`
	RolloverAdjustment string  `json:"rollover_adjustment"`
	Budget             string  `json:"budget"`
	Spending           string  `json:"spending"`
	Remaining          string  `json:"remaining"`
	SpentOfBudget      *string `json:"spent_of_budget"`
	ShareOfBaseBudget  *string `json:"share_of_base_budget"`
	ShareOfSpending    *string `json:"share_of_spending"`
}

// MonthlySeriesCategory is one category cell in a category-by-month series.
// Budget-dependent fields are nullable when the month has no budget snapshot;
// spending facts remain available in that case.
type MonthlySeriesCategory struct {
	CategoryID         int64   `json:"category_id"`
	Category           string  `json:"category"`
	BaseBudget         *string `json:"base_budget"`
	RolloverAdjustment *string `json:"rollover_adjustment"`
	SinkingFund        bool    `json:"sinking_fund"`
	SinkingFundOpening *string `json:"sinking_fund_opening_balance"`
	Budget             *string `json:"budget"`
	Spending           string  `json:"spending"`
	Remaining          *string `json:"remaining"`
	SpentOfBudget      *string `json:"spent_of_budget"`
	ShareOfBaseBudget  *string `json:"share_of_base_budget"`
	ShareOfSpending    *string `json:"share_of_spending"`
	TransactionCount   int64   `json:"transaction_count"`
}

type CategorySummary struct {
	CategoryID         int64   `json:"category_id"`
	Category           string  `json:"category"`
	Month              string  `json:"month"`
	BaseBudget         string  `json:"base_budget"`
	SinkingFund        bool    `json:"sinking_fund"`
	SinkingFundOpening string  `json:"sinking_fund_opening_balance"`
	RolloverAdjustment string  `json:"rollover_adjustment"`
	Budget             string  `json:"budget"`
	TotalSpending      string  `json:"total_spending"`
	Remaining          string  `json:"remaining"`
	SpentOfBudget      *string `json:"spent_of_budget"`
	TransactionCount   int64   `json:"transaction_count"`
}

type ComparisonMonth struct {
	Month                   string `json:"month"`
	TotalBaseBudget         string `json:"total_base_budget"`
	TotalSinkingFundOpening string `json:"total_sinking_fund_opening_balance"`
	TotalRolloverAdjustment string `json:"total_rollover_adjustment"`
	TotalBudget             string `json:"total_budget"`
	TotalSpending           string `json:"total_spending"`
	Remaining               string `json:"remaining"`
}

type ComparisonChange struct {
	TotalBaseBudget         string `json:"total_base_budget"`
	TotalSinkingFundOpening string `json:"total_sinking_fund_opening_balance"`
	TotalRolloverAdjustment string `json:"total_rollover_adjustment"`
	TotalBudget             string `json:"total_budget"`
	TotalSpending           string `json:"total_spending"`
	Remaining               string `json:"remaining"`
}

type ComparisonCategory struct {
	CategoryID               int64  `json:"category_id"`
	Category                 string `json:"category"`
	FromBaseBudget           string `json:"from_base_budget"`
	ToBaseBudget             string `json:"to_base_budget"`
	BaseBudgetChange         string `json:"base_budget_change"`
	FromSinkingFundOpening   string `json:"from_sinking_fund_opening_balance"`
	ToSinkingFundOpening     string `json:"to_sinking_fund_opening_balance"`
	SinkingFundOpeningChange string `json:"sinking_fund_opening_balance_change"`
	FromSinkingFund          bool   `json:"from_sinking_fund"`
	ToSinkingFund            bool   `json:"to_sinking_fund"`
	FromRolloverAdjustment   string `json:"from_rollover_adjustment"`
	ToRolloverAdjustment     string `json:"to_rollover_adjustment"`
	RolloverAdjustmentChange string `json:"rollover_adjustment_change"`
	FromBudget               string `json:"from_budget"`
	ToBudget                 string `json:"to_budget"`
	BudgetChange             string `json:"budget_change"`
	FromSpending             string `json:"from_spending"`
	ToSpending               string `json:"to_spending"`
	SpendingChange           string `json:"spending_change"`
}

type SpendingSummaryCategory struct {
	CategoryID       int64  `json:"category_id"`
	Category         string `json:"category"`
	Spending         string `json:"spending"`
	TransactionCount int64  `json:"transaction_count"`
}

type MerchantSpending struct {
	Merchant         string `json:"merchant"`
	Spending         string `json:"spending"`
	TransactionCount int64  `json:"transaction_count"`
}

type RecurringTransaction struct {
	ID             int64   `json:"id"`
	Merchant       string  `json:"merchant"`
	Amount         string  `json:"amount"`
	CategoryID     int64   `json:"category_id"`
	Category       string  `json:"category"`
	CategoryActive bool    `json:"category_active"`
	DayOfMonth     int64   `json:"day_of_month"`
	Note           *string `json:"note"`
	Active         bool    `json:"active"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

type DueTransaction struct {
	RecurringTransactionID int64   `json:"recurring_transaction_id"`
	Merchant               string  `json:"merchant"`
	Amount                 string  `json:"amount"`
	CategoryID             int64   `json:"category_id"`
	Category               string  `json:"category"`
	DueDate                string  `json:"due_date"`
	Note                   *string `json:"note"`
}

type UpcomingTransaction struct {
	RecurringTransactionID int64   `json:"recurring_transaction_id"`
	Merchant               string  `json:"merchant"`
	Amount                 string  `json:"amount"`
	CategoryID             int64   `json:"category_id"`
	Category               string  `json:"category"`
	ScheduledDate          string  `json:"scheduled_date"`
	Status                 string  `json:"status"`
	Note                   *string `json:"note"`
}

type BlockedDueTransaction struct {
	RecurringTransactionID int64  `json:"recurring_transaction_id"`
	Merchant               string `json:"merchant"`
	Category               string `json:"category"`
	DueDate                string `json:"due_date"`
	Reason                 string `json:"reason"`
}

type SavingsGoal struct {
	ID                int64   `json:"id"`
	Name              string  `json:"name"`
	AccountID         int64   `json:"account_id"`
	Account           string  `json:"account"`
	TargetAmount      string  `json:"target_amount"`
	TargetDate        *string `json:"target_date"`
	CurrentAmount     string  `json:"current_amount"`
	RemainingAmount   string  `json:"remaining_amount"`
	AmountAboveTarget *string `json:"amount_above_target,omitempty"`
	ProgressPercent   string  `json:"progress_percent"`
	TargetReached     bool    `json:"target_reached"`
	Status            string  `json:"status"`
	Note              *string `json:"note"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
	CompletedAt       *string `json:"completed_at"`
	CancelledAt       *string `json:"cancelled_at"`
}
