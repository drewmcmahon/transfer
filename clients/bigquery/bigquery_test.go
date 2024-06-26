package bigquery

import "github.com/stretchr/testify/assert"

func (b *BigQueryTestSuite) TestTableRelName() {
	{
		relName, err := tableRelName("project.dataset.table")
		assert.NoError(b.T(), err)
		assert.Equal(b.T(), "table", relName)
	}
	{
		relName, err := tableRelName("project.dataset.table.table")
		assert.NoError(b.T(), err)
		assert.Equal(b.T(), "table.table", relName)
	}
	{
		// All the possible errors
		_, err := tableRelName("project.dataset")
		assert.ErrorContains(b.T(), err, "invalid fully qualified name: project.dataset")

		_, err = tableRelName("project")
		assert.ErrorContains(b.T(), err, "invalid fully qualified name: project")
	}
}
