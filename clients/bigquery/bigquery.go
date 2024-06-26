package bigquery

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"cloud.google.com/go/bigquery"
	_ "github.com/viant/bigquery"

	"github.com/artie-labs/transfer/clients/shared"
	"github.com/artie-labs/transfer/lib/config"
	"github.com/artie-labs/transfer/lib/config/constants"
	"github.com/artie-labs/transfer/lib/db"
	"github.com/artie-labs/transfer/lib/destination/ddl"
	"github.com/artie-labs/transfer/lib/destination/types"
	"github.com/artie-labs/transfer/lib/logger"
	"github.com/artie-labs/transfer/lib/optimization"
	"github.com/artie-labs/transfer/lib/ptr"
)

const (
	GooglePathToCredentialsEnvKey = "GOOGLE_APPLICATION_CREDENTIALS"
	describeNameCol               = "column_name"
	describeTypeCol               = "data_type"
	describeCommentCol            = "description"
)

type Store struct {
	configMap *types.DwhToTablesConfigMap
	batchSize int
	config    config.Config

	db.Store
}

func (s *Store) PrepareTemporaryTable(tableData *optimization.TableData, tableConfig *types.DwhTableConfig, tempTableName string, _ types.AdditionalSettings, createTempTable bool) error {
	if createTempTable {
		tempAlterTableArgs := ddl.AlterTableArgs{
			Dwh:               s,
			Tc:                tableConfig,
			FqTableName:       tempTableName,
			CreateTable:       true,
			TemporaryTable:    true,
			ColumnOp:          constants.Add,
			UppercaseEscNames: &s.config.SharedDestinationConfig.UppercaseEscapedNames,
			Mode:              tableData.Mode(),
		}

		if err := tempAlterTableArgs.AlterTable(tableData.ReadOnlyInMemoryCols().GetColumns()...); err != nil {
			return fmt.Errorf("failed to create temp table: %w", err)
		}
	}

	// Cast the data into BigQuery values
	var rows []*Row
	additionalDateFmts := s.config.SharedTransferConfig.TypingSettings.AdditionalDateFormats
	for _, value := range tableData.Rows() {
		data := make(map[string]bigquery.Value)
		for _, col := range tableData.ReadOnlyInMemoryCols().GetColumnsToUpdate(s.config.SharedDestinationConfig.UppercaseEscapedNames, nil) {
			colKind, _ := tableData.ReadOnlyInMemoryCols().GetColumn(col)
			colVal, err := castColVal(value[col], colKind, additionalDateFmts)
			if err != nil {
				return fmt.Errorf("failed to cast col %s: %w", col, err)
			}

			if colVal != nil {
				data[col] = colVal
			}
		}

		rows = append(rows, NewRow(data))
	}

	// Load the data
	return s.putTable(context.Background(), tableData.TopicConfig.Database, tempTableName, rows)
}

func (s *Store) ToFullyQualifiedName(tableData *optimization.TableData, escape bool) string {
	return tableData.ToFqName(s.Label(), escape, s.config.SharedDestinationConfig.UppercaseEscapedNames,
		optimization.FqNameOpts{BigQueryProjectID: s.config.BigQuery.ProjectID})
}

func (s *Store) GetTableConfig(tableData *optimization.TableData) (*types.DwhTableConfig, error) {
	query := fmt.Sprintf("SELECT column_name, data_type, description FROM `%s.INFORMATION_SCHEMA.COLUMN_FIELD_PATHS` WHERE table_name = ?;", tableData.TopicConfig.Database)
	return shared.GetTableCfgArgs{
		Dwh:                s,
		FqName:             s.ToFullyQualifiedName(tableData, true),
		ConfigMap:          s.configMap,
		Query:              query,
		Args:               []any{tableData.RawName()},
		ColumnNameLabel:    describeNameCol,
		ColumnTypeLabel:    describeTypeCol,
		ColumnDescLabel:    describeCommentCol,
		EmptyCommentValue:  ptr.ToString(""),
		DropDeletedColumns: tableData.TopicConfig.DropDeletedColumns,
	}.GetTableConfig()
}

func (s *Store) GetConfigMap() *types.DwhToTablesConfigMap {
	if s == nil {
		return nil
	}

	return s.configMap
}

func (s *Store) Label() constants.DestinationKind {
	return constants.BigQuery
}

func (s *Store) GetClient(ctx context.Context) *bigquery.Client {
	client, err := bigquery.NewClient(ctx, s.config.BigQuery.ProjectID)
	if err != nil {
		logger.Panic("Failed to get bigquery client", slog.Any("err", err))
	}

	return client
}

func tableRelName(fqName string) (string, error) {
	fqNameParts := strings.Split(fqName, ".")
	if len(fqNameParts) < 3 {
		return "", fmt.Errorf("invalid fully qualified name: %s", fqName)
	}

	return strings.Join(fqNameParts[2:], "."), nil
}

func (s *Store) putTable(ctx context.Context, dataset, tableName string, rows []*Row) error {
	relTableName, err := tableRelName(tableName)
	if err != nil {
		return fmt.Errorf("failed to get table name: %w", err)
	}

	client := s.GetClient(ctx)
	defer client.Close()

	batch := NewBatch(rows, s.batchSize)
	inserter := client.Dataset(dataset).Table(relTableName).Inserter()
	for batch.HasNext() {
		if err = inserter.Put(ctx, batch.NextChunk()); err != nil {
			return fmt.Errorf("failed to insert rows: %w", err)
		}
	}

	return nil
}

func (s *Store) Dedupe(fqTableName string) error {
	return fmt.Errorf("dedupe is not yet implemented")
}

func LoadBigQuery(cfg config.Config, _store *db.Store) *Store {
	cfg.BigQuery.LoadDefaultValues()
	if _store != nil {
		// Used for tests.
		return &Store{
			Store: *_store,

			configMap: &types.DwhToTablesConfigMap{},
			config:    cfg,
		}
	}

	if credPath := cfg.BigQuery.PathToCredentials; credPath != "" {
		// If the credPath is set, let's set it into the env var.
		slog.Debug("Writing the path to BQ credentials to env var for google auth")
		err := os.Setenv(GooglePathToCredentialsEnvKey, credPath)
		if err != nil {
			logger.Panic(fmt.Sprintf("Error setting env var for %s", GooglePathToCredentialsEnvKey), slog.Any("err", err))
		}
	}

	return &Store{
		Store:     db.Open("bigquery", cfg.BigQuery.DSN()),
		configMap: &types.DwhToTablesConfigMap{},
		batchSize: cfg.BigQuery.BatchSize,
		config:    cfg,
	}
}
