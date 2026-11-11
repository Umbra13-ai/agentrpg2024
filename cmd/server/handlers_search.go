package main



// Paginated response wrapper
type PaginatedResponse struct {
	Data       interface{} `json:"data"`
	Count      int         `json:"count"`
	Total      int         `json:"total"`
	Page       int         `json:"page"`
	PerPage    int         `json:"per_page"`
	TotalPages int         `json:"total_pages"`
	HasMore    bool        `json:"has_more"`
}

