package handler

import (
	"net/http"

	"github.com/Astemirdum/library-service/gateway/config"
	"github.com/Astemirdum/library-service/gateway/internal/errs"
	"github.com/Astemirdum/library-service/gateway/internal/model"
	"github.com/Astemirdum/library-service/gateway/internal/service/library"
	"github.com/Astemirdum/library-service/gateway/internal/service/rating"
	"github.com/Astemirdum/library-service/gateway/internal/service/reservation"
	"github.com/Astemirdum/library-service/pkg/validate"
	_ "github.com/Astemirdum/library-service/swagger"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	echoSwagger "github.com/swaggo/echo-swagger"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Handler struct {
	librarySvc     LibraryService
	ratingSvc      RatingService
	reservationSvc ReservationService
	// client         *http.Client
	log *zap.Logger
}

func New(log *zap.Logger, cfg config.Config) *Handler {
	h := &Handler{
		librarySvc:     library.NewService(log, cfg),
		ratingSvc:      rating.NewService(log, cfg),
		reservationSvc: reservation.NewService(log, cfg),
		// client:         &http.Client{Timeout: time.Minute},
		log: log,
	}
	return h
}

func (h *Handler) NewRouter() *echo.Echo {
	e := echo.New()
	const (
		baseRPS = 10
		apiRPS  = 100
	)
	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		StackSize: 4 << 10, // 4 KB
	}))
	e.Use(middleware.Recover())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{http.MethodGet, http.MethodOptions, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodPost, http.MethodDelete},
		AllowCredentials: true,
	}))

	base := e.Group("", newRateLimiterMW(baseRPS))
	base.GET("/manage/health", h.Health)
	base.GET("/swagger/*", echoSwagger.WrapHandler)

	e.Validator = validate.NewCustomValidator()
	api := e.Group("/api/v1",
		middleware.RequestLoggerWithConfig(requestLoggerConfig()),
		middleware.RequestID(),
		newRateLimiterMW(apiRPS),
	)

	api.GET("/rating", h.GetRating)

	api.GET("/libraries", h.GetLibraries)
	api.GET("/libraries/:libraryUid/books", h.GetBooks)

	api.POST("/reservations", h.CreateReservation)
	api.GET("/reservations", h.GetReservations)
	api.POST("/reservations/:reservationUid/return", h.ReservationReturn)

	return e
}

func (h *Handler) Health(c echo.Context) error {
	return c.String(http.StatusOK, "OK")
}

const (
	XUserName = "X-User-Name"
)

func (h *Handler) GetReservations(c echo.Context) error {
	userName := c.Request().Header.Get(XUserName)
	if userName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, errs.ErrUserName)
	}
	ctx := c.Request().Context()
	reserves, code, err := h.reservationSvc.GetReservation(ctx, userName)
	if err != nil {
		return echo.NewHTTPError(code, err.Error())
	}

	gg, ctx := errgroup.WithContext(ctx)
	libs := make([]model.Library, 0, len(reserves))
	gg.Go(func() error {
		for _, reserve := range reserves {
			lib, code, err := h.librarySvc.GetLibrary(ctx, reserve.LibraryUid)
			if err != nil {
				return echo.NewHTTPError(code, err.Error())
			}
			libs = append(libs, lib.Library)
		}
		return nil
	})
	books := make([]model.Book, 0, len(reserves))
	gg.Go(func() error {
		for _, reserve := range reserves {
			book, code, err := h.librarySvc.GetBook(ctx, reserve.LibraryUid, reserve.BookUid)
			if err != nil {
				return echo.NewHTTPError(code, err.Error())
			}
			books = append(books, book.Book)
		}
		return nil
	})

	if err := gg.Wait(); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, getReservationResponse(reserves, books, libs))
}

func getReservationResponse(reserves []model.GetReservation, books []model.Book, libs []model.Library) []model.GetReservationResponse {
	items := make([]model.GetReservationResponse, 0, len(reserves))
	for i := range reserves {
		items = append(items, model.GetReservationResponse{
			Reservation: model.Reservation{
				ReservationUid: reserves[i].ReservationUid,
				Status:         reserves[i].Status,
				StartDate:      reserves[i].StartDate,
				TillDate:       reserves[i].TillDate,
			},
			Library: libs[i],
			Book:    books[i],
		})
	}
	return items
}

func (h *Handler) CreateReservation(c echo.Context) error {
	var createReservationRequest model.CreateReservationRequest
	if err := c.Bind(&createReservationRequest); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	createReservationRequest.UserName = c.Request().Header.Get(XUserName)
	if err := c.Validate(createReservationRequest); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	ctx := c.Request().Context()
	var (
		lib  model.GetLibrary
		book model.GetBook
		rat  model.Rating
		code int
		err  error
	)
	gg, ctxCancel := errgroup.WithContext(ctx)
	gg.Go(func() error {
		lib, code, err = h.librarySvc.GetLibrary(ctxCancel, createReservationRequest.LibraryUid)
		if err != nil {
			return echo.NewHTTPError(code, err.Error())
		}
		return nil
	})

	gg.Go(func() error {
		book, code, err = h.librarySvc.GetBook(ctxCancel, createReservationRequest.LibraryUid, createReservationRequest.BookUid)
		if err != nil {
			return echo.NewHTTPError(code, err.Error())
		}
		return nil
	})

	gg.Go(func() error {
		rat, code, err = h.ratingSvc.GetRating(ctxCancel, createReservationRequest.UserName)
		if err != nil {
			return echo.NewHTTPError(code, err.Error())
		}
		return nil
	})

	if err := gg.Wait(); err != nil {
		return err
	}
	createReservationRequest.Stars = rat.Stars
	rsv, code, err := h.reservationSvc.CreateReservation(ctx, createReservationRequest)
	if err != nil {
		return echo.NewHTTPError(code, err.Error())
	}

	if code, err := h.librarySvc.AvailableCount(ctx, lib.ID, book.ID, false); err != nil {
		return echo.NewHTTPError(code, err.Error())
	}

	return c.JSON(http.StatusOK, model.CreateReservationResponse{
		ReservationUid: rsv.ReservationUid,
		Status:         rsv.Status,
		StartDate:      model.Date2{Time: rsv.StartDate},
		TillDate:       model.Date2{Time: rsv.TillDate},
		Library:        lib.Library,
		Book:           book.Book,
		Rating:         rat,
	})
}

func (h *Handler) ReservationReturn(c echo.Context) error {
	ctx := c.Request().Context()
	username := c.Request().Header.Get(XUserName)
	if username == "" {
		return echo.NewHTTPError(http.StatusBadRequest, errs.ErrUserName)
	}
	reservationUid := c.Param("reservationUid")
	var req model.ReservationReturnRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	res, code, err := h.reservationSvc.ReservationReturn(ctx, req, username, reservationUid)
	if err != nil {
		return echo.NewHTTPError(code, err.Error())
	}
	var (
		lib  model.GetLibrary
		book model.GetBook
	)
	gg, ctxCancel := errgroup.WithContext(ctx)
	gg.Go(func() error {
		lib, code, err = h.librarySvc.GetLibrary(ctxCancel, res.LibraryUid)
		if err != nil {
			return echo.NewHTTPError(code, err.Error())
		}
		return nil
	})

	gg.Go(func() error {
		book, code, err = h.librarySvc.GetBook(ctxCancel, res.LibraryUid, res.BookUid)
		if err != nil {
			return echo.NewHTTPError(code, err.Error())
		}
		return nil
	})
	if err := gg.Wait(); err != nil {
		return err
	}

	if code, err := h.librarySvc.AvailableCount(ctx, lib.ID, book.ID, true); err != nil {
		return echo.NewHTTPError(code, err.Error())
	}

	stars := 1
	if book.Condition != req.Condition {
		stars = -10
	}
	if code, err := h.ratingSvc.Rating(ctx, username, stars); err != nil {
		return echo.NewHTTPError(code, err.Error())
	}

	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) GetBooks(c echo.Context) error {
	data, code, err := h.librarySvc.GetBooks(c)
	if err != nil {
		return echo.NewHTTPError(code, err.Error())
	}
	return c.JSONBlob(code, data)
}

func (h *Handler) GetRating(c echo.Context) error {
	resp, code, err := h.ratingSvc.GetRating(c.Request().Context(), c.Request().Header.Get(XUserName))
	if err != nil {
		return echo.NewHTTPError(code, err.Error())
	}
	return c.JSON(code, resp)
}

func (h *Handler) GetLibraries(c echo.Context) error {
	data, code, err := h.librarySvc.GetLibraries(c)
	if err != nil {
		return echo.NewHTTPError(code, err.Error())
	}
	return c.JSONBlob(code, data)
}
