package seats

type Seat struct {
	ID      int64
	Status  status
	EventID int64
}

type status string

const (
	SeatAvailabe status = "available"
	SeatBooked   status = "booked"
	SeatSold     status = "sold"
)
