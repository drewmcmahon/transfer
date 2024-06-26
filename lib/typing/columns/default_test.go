package columns

import (
	"fmt"
	"testing"
	"time"

	"github.com/artie-labs/transfer/lib/config/constants"

	"github.com/artie-labs/transfer/lib/typing/ext"

	"github.com/artie-labs/transfer/lib/typing"

	"github.com/stretchr/testify/assert"
)

func TestColumn_DefaultValue(t *testing.T) {
	birthday := time.Date(2022, time.September, 6, 3, 19, 24, 942000000, time.UTC)
	birthdayExtDateTime, err := ext.ParseExtendedDateTime(birthday.Format(ext.ISO8601), nil)
	assert.NoError(t, err)

	// date
	dateKind := typing.ETime
	dateKind.ExtendedTimeDetails = &ext.Date
	// time
	timeKind := typing.ETime
	timeKind.ExtendedTimeDetails = &ext.Time
	// date time
	dateTimeKind := typing.ETime
	dateTimeKind.ExtendedTimeDetails = &ext.DateTime

	testCases := []struct {
		name                       string
		col                        *Column
		args                       *DefaultValueArgs
		expectedValue              any
		destKindToExpectedValueMap map[constants.DestinationKind]any
	}{
		{
			name: "default value = nil",
			col: &Column{
				KindDetails:  typing.String,
				defaultValue: nil,
			},
			args: &DefaultValueArgs{
				Escape: true,
			},
			expectedValue: nil,
		},
		{
			name: "escaped args (nil)",
			col: &Column{
				KindDetails:  typing.String,
				defaultValue: "abcdef",
			},
			expectedValue: "abcdef",
		},
		{
			name: "escaped args (escaped = false)",
			col: &Column{
				KindDetails:  typing.String,
				defaultValue: "abcdef",
			},
			args:          &DefaultValueArgs{},
			expectedValue: "abcdef",
		},
		{
			name: "string",
			col: &Column{
				KindDetails:  typing.String,
				defaultValue: "abcdef",
			},
			args: &DefaultValueArgs{
				Escape: true,
			},
			expectedValue: "'abcdef'",
		},
		{
			name: "json",
			col: &Column{
				KindDetails:  typing.Struct,
				defaultValue: "{}",
			},
			args: &DefaultValueArgs{
				Escape: true,
			},
			expectedValue: `{}`,
			destKindToExpectedValueMap: map[constants.DestinationKind]any{
				constants.BigQuery:  "JSON'{}'",
				constants.Redshift:  `JSON_PARSE('{}')`,
				constants.Snowflake: `'{}'`,
			},
		},
		{
			name: "json w/ some values",
			col: &Column{
				KindDetails:  typing.Struct,
				defaultValue: "{\"age\": 0, \"membership_level\": \"standard\"}",
			},
			args: &DefaultValueArgs{
				Escape: true,
			},
			expectedValue: "{\"age\": 0, \"membership_level\": \"standard\"}",
			destKindToExpectedValueMap: map[constants.DestinationKind]any{
				constants.BigQuery:  "JSON'{\"age\": 0, \"membership_level\": \"standard\"}'",
				constants.Redshift:  "JSON_PARSE('{\"age\": 0, \"membership_level\": \"standard\"}')",
				constants.Snowflake: "'{\"age\": 0, \"membership_level\": \"standard\"}'",
			},
		},
		{
			name: "date",
			col: &Column{
				KindDetails:  dateKind,
				defaultValue: birthdayExtDateTime,
			},
			args: &DefaultValueArgs{
				Escape: true,
			},
			expectedValue: "'2022-09-06'",
		},
		{
			name: "time",
			col: &Column{
				KindDetails:  timeKind,
				defaultValue: birthdayExtDateTime,
			},
			args: &DefaultValueArgs{
				Escape: true,
			},
			expectedValue: "'03:19:24'",
		},
		{
			name: "datetime",
			col: &Column{
				KindDetails:  dateTimeKind,
				defaultValue: birthdayExtDateTime,
			},
			args: &DefaultValueArgs{
				Escape: true,
			},
			expectedValue: "'2022-09-06T03:19:24Z'",
		},
	}

	for _, testCase := range testCases {
		for _, validDest := range constants.ValidDestinations {
			if testCase.args != nil {
				testCase.args.DestKind = validDest
			}

			actualValue, actualErr := testCase.col.DefaultValue(testCase.args, nil)
			assert.NoError(t, actualErr, fmt.Sprintf("%s %s", testCase.name, validDest))

			expectedValue := testCase.expectedValue
			if potentialValue, isOk := testCase.destKindToExpectedValueMap[validDest]; isOk {
				// Not everything requires a destination specific value, so only use this if necessary.
				expectedValue = potentialValue
			}

			assert.Equal(t, expectedValue, actualValue, fmt.Sprintf("%s %s", testCase.name, validDest))
		}
	}
}
