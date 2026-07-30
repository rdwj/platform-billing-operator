package ledger

import "context"

type Querier struct {
	store *Store
}

func NewQuerier(store *Store) *Querier {
	return &Querier{store: store}
}

func (q *Querier) QueryEvents(ctx context.Context, filter QueryFilter) (*QueryResult, error) {
	return q.store.QueryEvents(ctx, filter)
}
