package syncx

type Broadcaster[T any] struct {
	subscribers []chan T
}

func NewBroadcaster[T any]() *Broadcaster[T] {
	return &Broadcaster[T]{
		subscribers: make([]chan T, 0),
	}
}

func (b *Broadcaster[T]) Subscribe() chan T {
	ch := make(chan T)
	b.subscribers = append(b.subscribers, ch)
	return ch
}

func (b *Broadcaster[T]) Unsubscribe(ch chan T) {
	defer close(ch)

	for i, sub := range b.subscribers {
		if sub == ch {
			b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
			return
		}
	}
}

func (b *Broadcaster[T]) Emit(msg T) {
	for _, sub := range b.subscribers {
		go func(sub chan T) {
			sub <- msg
		}(sub)
	}
}

func (b *Broadcaster[T]) Close() {
	for _, sub := range b.subscribers {
		close(sub)
	}
}
