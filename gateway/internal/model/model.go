package model

import (
	"strings"
	"time"
)

type CreateReservationResponse struct {
	Reservation Reservation `json:",inline"`
	Library     Library     `json:"library"`
	Book        Book        `json:"book"`
	Rating      Rating      `json:"rating"`
}

type GetReservationResponse struct {
	Reservation Reservation `json:",inline"`
	Library     Library     `json:"library"`
	Book        Book        `json:"book"`
}

type CreateReservationRequest struct {
	BookUid    string `json:"bookUid" validate:"required"`
	LibraryUid string `json:"libraryUid" validate:"required"`
	TillDate   Date   `json:"tillDate" validate:"required"`
	UserName   string `json:"-" validate:"required"`
}

type Date struct {
	time.Time `json:",inline"`
}

func (d *Date) UnmarshalJSON(b []byte) (err error) {
	s := strings.Trim(string(b), "\"")
	date, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return err
	}
	d.Time = date
	return
}

//func (d *Date) MarshalJSON() ([]byte, error) {
//	return []byte(fmt.Sprintf(`"%s"`, d.Format(time.DateOnly))), nil
//}

type Library struct {
	LibraryUid string `json:"libraryUid"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	City       string `json:"city"`
}
type Book struct {
	BookUid string `json:"bookUid"`
	Name    string `json:"name"`
	Author  string `json:"author"`
	Genre   string `json:"genre"`
}

type Reservation struct {
	ReservationUid string    `json:"reservationUid"`
	Status         string    `json:"status"`
	StartDate      time.Time `json:"startDate"`
	TillDate       time.Time `json:"tillDate"`
}

type GetReservation struct {
	ReservationUid string    `json:"reservationUid"`
	BookUid        string    `json:"bookUid"`
	LibraryUid     string    `json:"libraryUid"`
	Status         string    `json:"status"`
	StartDate      time.Time `json:"startDate"`
	TillDate       time.Time `json:"tillDate"`
}

type Rating struct {
	Stars int `json:"stars"`
}

type ReservationReturnRequest struct {
	Condition string `json:"condition" validate:"required,oneof=EXCELLENT GOOD BAD"`
	Date      Date   `json:"date" validate:"required"`
}
