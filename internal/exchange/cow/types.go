package cow

// CowOrder represents an order submitted to the CoW Protocol API.
type CowOrder struct {
	SellToken         string `json:"sellToken"`
	BuyToken          string `json:"buyToken"`
	SellAmount        string `json:"sellAmount"`
	BuyAmount         string `json:"buyAmount"`
	ValidTo           int64  `json:"validTo"`
	AppData           string `json:"appData"`
	FeeAmount         string `json:"feeAmount"`
	Kind              string `json:"kind"`
	PartiallyFillable bool   `json:"partiallyFillable"`
	Signature         string `json:"signature"`
	SigningScheme     string `json:"signingScheme"`
}

// CowQuoteRequest is sent to POST /api/v1/quote to get a price quote.
type CowQuoteRequest struct {
	SellToken            string `json:"sellToken"`
	BuyToken             string `json:"buyToken"`
	SellAmountBeforeFee  string `json:"sellAmountBeforeFee"`
	Kind                 string `json:"kind"`
	From                 string `json:"from"`
}

// CowQuoteResponse is returned from the quote endpoint.
type CowQuoteResponse struct {
	Quote CowQuote `json:"quote"`
	From  string   `json:"from"`
	ID    int      `json:"id"`
}

// CowQuote holds the price and fee details within a quote response.
type CowQuote struct {
	SellAmount string `json:"sellAmount"`
	BuyAmount  string `json:"buyAmount"`
	FeeAmount  string `json:"feeAmount"`
}

// CowOrderResponse is the UID returned after submitting an order.
type CowOrderResponse string

// CowOrderStatusResponse represents the order status returned from the API.
type CowOrderStatusResponse struct {
	UID               string `json:"uid"`
	Status            string `json:"status"`
	ExecutedSellAmount string `json:"executedSellAmount"`
	ExecutedBuyAmount  string `json:"executedBuyAmount"`
}

// CowTrade represents a single trade from the trades endpoint.
type CowTrade struct {
	OrderUID        string `json:"orderUid"`
	Owner           string `json:"owner"`
	SellAmount      string `json:"sellAmount"`
	BuyAmount       string `json:"buyAmount"`
	SellToken       string `json:"sellToken"`
	BuyToken        string `json:"buyToken"`
	ExecutionDate   string `json:"executionDate"`
}

// CowErrorResponse represents an error returned from the CoW Protocol API.
type CowErrorResponse struct {
	ErrorType   string `json:"errorType"`
	Description string `json:"description"`
}
