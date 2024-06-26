package snowflake

import (
	"fmt"
	"log/slog"

	"github.com/snowflakedb/gosnowflake"

	"github.com/artie-labs/transfer/clients/shared"
	"github.com/artie-labs/transfer/lib/config"
	"github.com/artie-labs/transfer/lib/config/constants"
	"github.com/artie-labs/transfer/lib/db"
	"github.com/artie-labs/transfer/lib/destination/types"
	"github.com/artie-labs/transfer/lib/kafkalib"
	"github.com/artie-labs/transfer/lib/logger"
	"github.com/artie-labs/transfer/lib/optimization"
	"github.com/artie-labs/transfer/lib/ptr"
)

const maxRetries = 10

type Store struct {
	db.Store
	testDB    bool // Used for testing
	configMap *types.DwhToTablesConfigMap
	config    config.Config
}

const (
	// Column names from the output of DESC table;
	describeNameCol    = "name"
	describeTypeCol    = "type"
	describeCommentCol = "comment"
)

func (s *Store) ToFullyQualifiedName(tableData *optimization.TableData, escape bool) string {
	return tableData.ToFqName(s.Label(), escape, s.config.SharedDestinationConfig.UppercaseEscapedNames, optimization.FqNameOpts{})
}

func (s *Store) GetTableConfig(tableData *optimization.TableData) (*types.DwhTableConfig, error) {
	fqName := s.ToFullyQualifiedName(tableData, true)
	return shared.GetTableCfgArgs{
		Dwh:                s,
		FqName:             fqName,
		ConfigMap:          s.configMap,
		Query:              fmt.Sprintf("DESC TABLE %s;", fqName),
		ColumnNameLabel:    describeNameCol,
		ColumnTypeLabel:    describeTypeCol,
		ColumnDescLabel:    describeCommentCol,
		EmptyCommentValue:  ptr.ToString("<nil>"),
		DropDeletedColumns: tableData.TopicConfig.DropDeletedColumns,
	}.GetTableConfig()
}

func (s *Store) Sweep() error {
	tcs, err := s.config.TopicConfigs()
	if err != nil {
		return err
	}

	queryFunc := func(dbAndSchemaPair kafkalib.DatabaseSchemaPair) (string, []any) {
		return fmt.Sprintf(`
SELECT
    table_schema, table_name
FROM
    %s.information_schema.tables
WHERE
    UPPER(table_schema) = UPPER(?) AND table_name ILIKE ?`, dbAndSchemaPair.Database), []any{dbAndSchemaPair.Schema, "%" + constants.ArtiePrefix + "%"}
	}

	return shared.Sweep(s, tcs, queryFunc)
}

func (s *Store) Label() constants.DestinationKind {
	return constants.Snowflake
}

func (s *Store) GetConfigMap() *types.DwhToTablesConfigMap {
	if s == nil {
		return nil
	}

	return s.configMap
}

func (s *Store) reestablishConnection() {
	if s.testDB {
		// Don't actually re-establish for tests.
		return
	}

	cfg := &gosnowflake.Config{
		Account:     s.config.Snowflake.AccountID,
		User:        s.config.Snowflake.Username,
		Password:    s.config.Snowflake.Password,
		Warehouse:   s.config.Snowflake.Warehouse,
		Region:      s.config.Snowflake.Region,
		Application: s.config.Snowflake.Application,
	}

	if s.config.Snowflake.Host != "" {
		// If the host is specified
		cfg.Host = s.config.Snowflake.Host
		cfg.Region = ""
	}

	dsn, err := gosnowflake.DSN(cfg)
	if err != nil {
		logger.Panic("Failed to get snowflake dsn", slog.Any("err", err))
	}

	s.Store = db.Open("snowflake", dsn)
}

func (s *Store) Dedupe(fqTableName string) error {
	_, err := s.Exec(fmt.Sprintf("CREATE OR REPLACE TABLE %s AS SELECT DISTINCT * FROM %s", fqTableName, fqTableName))
	return err
}

func LoadSnowflake(cfg config.Config, _store *db.Store) *Store {
	if _store != nil {
		// Used for tests.
		return &Store{
			testDB:    true,
			configMap: &types.DwhToTablesConfigMap{},
			config:    cfg,

			Store: *_store,
		}
	}

	s := &Store{
		configMap: &types.DwhToTablesConfigMap{},
		config:    cfg,
	}

	s.reestablishConnection()
	return s
}
