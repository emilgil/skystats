package main

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

func MarkProcessed(pg *postgres, colName string, aircrafts []Aircraft) {

	batch := &pgx.Batch{}

	for _, aircraft := range aircrafts {
		updateStatement := `UPDATE aircraft_data SET ` + colName + ` = true WHERE id = $1`
		batch.Queue(updateStatement, aircraft.Id)
	}

	br := pg.db.SendBatch(context.Background(), batch)
	defer br.Close()

	for i := 0; i < len(aircrafts); i++ {
		_, err := br.Exec()
		if err != nil {
			log.Error().Err(err).Msg("MarkProcessed() - Unable to update data")
		}
	}
}
