package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"highload/internal/tickets"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
)

func main() {
	var wg sync.WaitGroup

	bookRequest := tickets.BookRequest{EventID: 1, SeatID: 1, UserID: 1}
	json, err := json.Marshal(bookRequest)

	statusOk, status500, status409 := new(atomic.Int64), new(atomic.Int64), new(atomic.Int64)

	if err != nil {
		log.Fatalln("cannot marshal bookRequest")
		return
	}

	for i := 0; i < 50; i++ {
		wg.Go(func() {
			reader := bytes.NewReader(json)
			resp, err := http.Post("http://localhost:8080/book", "application/json", reader)
			if err != nil {
				status500.Add(1)
			}

			switch resp.StatusCode {
			case 201:
				statusOk.Add(1)
			case 409:
				status409.Add(1)
			}

		})

	}

	wg.Wait()

	fmt.Println("Success (201): ", statusOk)
	fmt.Println("Conflict (409): ", status409)
	fmt.Println("Errors: ", status500)
}
