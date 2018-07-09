package api

import (
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/kubernetes/fake"
	"testing"
)

func TestEnsureNamespaceExists(t *testing.T) {
	nonExistingNamespace := "nonexistingnamespace"
	existingNamespace := "existingnamespace"
	fakeClient := fake.NewSimpleClientset(createNamespaceDef(existingNamespace, teamName))
	client := clientHolder{fakeClient}

	t.Run("Ensure not err when namespace already exists", func(t *testing.T) {
		ns, err := client.createNamespace(existingNamespace, teamName)

		assert.NoError(t, err)
		assert.Equal(t, ns.ObjectMeta.Name, existingNamespace)
	})

	t.Run("Ensure namespace created if namespace does not exist", func(t *testing.T) {
		ns, err := client.createNamespace(nonExistingNamespace, teamName)

		assert.NoError(t, err)
		assert.Equal(t, ns.ObjectMeta.Name, nonExistingNamespace)
	})

	t.Run("Ensure namespace created gets labeled with team name", func(t *testing.T) {
		ns, err := client.createNamespace(nonExistingNamespace, teamName)

		assert.NoError(t, err)
		assert.Equal(t, ns.ObjectMeta.Labels["team"], teamName)
	})
}
