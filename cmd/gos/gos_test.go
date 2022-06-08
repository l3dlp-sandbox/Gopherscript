package main

import (
	"os"
	"path"
	"testing"
	"time"

	G "github.com/debloat-dev/Gopherscript"
	"github.com/stretchr/testify/assert"
)

func TestCreateFile(t *testing.T) {

	//in the following tests token buckets are emptied before calling __createFile

	if testing.Short() {
		return
	}

	cases := []struct {
		name             string
		limitation       G.Limitation
		contentByteSize  int
		expectedDuration time.Duration
	}{
		{
			"<content's size> == <rate> == FS_WRITE_MIN_CHUNK_SIZE, should take ~ 1s",
			G.Limitation{Name: FS_WRITE_LIMIT_NAME, ByteRate: G.ByteRate(FS_WRITE_MIN_CHUNK_SIZE)},
			FS_WRITE_MIN_CHUNK_SIZE,
			time.Second,
		},
		{
			"<content's size> == half of (<rate> == FS_WRITE_MIN_CHUNK_SIZE), should take ~ 0.5s",
			G.Limitation{Name: FS_WRITE_LIMIT_NAME, ByteRate: G.ByteRate(FS_WRITE_MIN_CHUNK_SIZE)},
			FS_WRITE_MIN_CHUNK_SIZE / 2,
			time.Second / 2,
		},
		{
			"<content's size> == 2 * (<rate> == FS_WRITE_MIN_CHUNK_SIZE), should take ~ 2s",
			G.Limitation{Name: FS_WRITE_LIMIT_NAME, ByteRate: G.ByteRate(FS_WRITE_MIN_CHUNK_SIZE)},
			2 * FS_WRITE_MIN_CHUNK_SIZE,
			2 * time.Second,
		},

		{
			"<content's size> == <rate> == 2 * FS_WRITE_MIN_CHUNK_SIZE, should take ~ 1s",
			G.Limitation{Name: FS_WRITE_LIMIT_NAME, ByteRate: G.ByteRate(2 * FS_WRITE_MIN_CHUNK_SIZE)},
			2 * FS_WRITE_MIN_CHUNK_SIZE,
			time.Second,
		},
		{
			"<content's size> == half of (<rate> == 2 * FS_WRITE_MIN_CHUNK_SIZE), should take ~ 0.5s",
			G.Limitation{Name: FS_WRITE_LIMIT_NAME, ByteRate: G.ByteRate(2 * FS_WRITE_MIN_CHUNK_SIZE)},
			FS_WRITE_MIN_CHUNK_SIZE,
			time.Second / 2,
		},
		{
			"<content's size> == 2 * (<rate> == 2 * FS_WRITE_MIN_CHUNK_SIZE), should take ~ 2s",
			G.Limitation{Name: FS_WRITE_LIMIT_NAME, ByteRate: G.ByteRate(2 * FS_WRITE_MIN_CHUNK_SIZE)},
			4 * FS_WRITE_MIN_CHUNK_SIZE,
			2 * time.Second,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			fpath := G.Path(path.Join(tmpDir, "test_file.data"))
			b := make([]byte, testCase.contentByteSize)

			ctx := G.NewContext([]G.Permission{
				G.FilesystemPermission{G.CreatePerm, fpath},
			}, nil, []G.Limitation{testCase.limitation})
			ctx.Take(testCase.limitation.Name, int64(testCase.limitation.ByteRate))

			start := time.Now()
			assert.NoError(t, __createFile(ctx, fpath, b, DEFAULT_FILE_FMODE))
			assert.WithinDuration(t, start.Add(testCase.expectedDuration), time.Now(), 500*time.Millisecond)
		})
	}

}

func TestReadEntireFile(t *testing.T) {

	//in the following tests token buckets are emptied before calling __createFile

	if testing.Short() {
		return
	}

	cases := []struct {
		name             string
		limitation       G.Limitation
		contentByteSize  int
		expectedDuration time.Duration
	}{
		{
			"<content's size> == <rate> == FS_READ_MIN_CHUNK_SIZE, should take ~ 1s",
			G.Limitation{Name: FS_READ_LIMIT_NAME, ByteRate: G.ByteRate(FS_READ_MIN_CHUNK_SIZE)},
			FS_READ_MIN_CHUNK_SIZE,
			time.Second,
		},
		{
			"<content's size> == half of (<rate> == FS_READ_MIN_CHUNK_SIZE), should take ~ 0.5s",
			G.Limitation{Name: FS_READ_LIMIT_NAME, ByteRate: G.ByteRate(FS_READ_MIN_CHUNK_SIZE)},
			FS_READ_MIN_CHUNK_SIZE / 2,
			time.Second / 2,
		},
		{
			"<content's size> == 2 * (<rate> == FS_READ_MIN_CHUNK_SIZE), should take ~ 2s",
			G.Limitation{Name: FS_READ_LIMIT_NAME, ByteRate: G.ByteRate(FS_READ_MIN_CHUNK_SIZE)},
			2 * FS_READ_MIN_CHUNK_SIZE,
			2 * time.Second,
		},
		{
			"<content's size> == <rate> == 2 * FS_READ_MIN_CHUNK_SIZE, should take ~ 1s",
			G.Limitation{Name: FS_READ_LIMIT_NAME, ByteRate: G.ByteRate(2 * FS_READ_MIN_CHUNK_SIZE)},
			2 * FS_READ_MIN_CHUNK_SIZE,
			time.Second,
		},
		{
			"<content's size> == half of (<rate> == 2 * FS_READ_MIN_CHUNK_SIZE), should take ~ 0.5s",
			G.Limitation{Name: FS_READ_LIMIT_NAME, ByteRate: G.ByteRate(2 * FS_READ_MIN_CHUNK_SIZE)},
			FS_READ_MIN_CHUNK_SIZE,
			time.Second / 2,
		},
		{
			"<content's size> == 2 * (<rate> == 2 * FS_READ_MIN_CHUNK_SIZE), should take ~ 2s",
			G.Limitation{Name: FS_READ_LIMIT_NAME, ByteRate: G.ByteRate(2 * FS_READ_MIN_CHUNK_SIZE)},
			4 * FS_READ_MIN_CHUNK_SIZE,
			2 * time.Second,
		},
		{
			"<content's size> == FS_READ_MIN_CHUNK_SIZE == 2 * <rate>, should take ~ 2s",
			G.Limitation{Name: FS_READ_LIMIT_NAME, ByteRate: G.ByteRate(FS_READ_MIN_CHUNK_SIZE / 2)},
			FS_READ_MIN_CHUNK_SIZE,
			2 * time.Second,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			//create the file
			fpath := G.Path(path.Join(t.TempDir(), "test_file.data"))
			b := make([]byte, testCase.contentByteSize)
			err := os.WriteFile(string(fpath), b, 0400)
			assert.NoError(t, err)

			//read it
			ctx := G.NewContext([]G.Permission{
				G.FilesystemPermission{G.ReadPerm, G.Path(fpath)},
			}, nil, []G.Limitation{testCase.limitation})
			ctx.Take(testCase.limitation.Name, int64(testCase.limitation.ByteRate))

			start := time.Now()
			_, err = __readEntireFile(ctx, fpath)
			assert.NoError(t, err)
			assert.WithinDuration(t, start.Add(testCase.expectedDuration), time.Now(), 500*time.Millisecond)
		})
	}

}

func newStoreTestContext(fpath G.Path) *G.Context {
	return G.NewContext([]G.Permission{
		G.FilesystemPermission{G.ReadPerm, fpath},
		G.FilesystemPermission{G.CreatePerm, fpath},
	}, nil, []G.Limitation{
		{Name: FS_READ_LIMIT_NAME, ByteRate: FS_READ_MIN_CHUNK_SIZE},
		{Name: FS_WRITE_LIMIT_NAME, ByteRate: FS_WRITE_MIN_CHUNK_SIZE},
	})
}

func TestOpenOrCreateStore(t *testing.T) {

	t.Run("open non existing store", func(t *testing.T) {
		fpath := G.Path(path.Join(t.TempDir(), "db.json"))

		ctx := newStoreTestContext(fpath)

		store, err := OpenOrCreateStore(ctx, fpath)

		assert.NoError(t, err)
		assert.NotNil(t, store)
	})

	t.Run("create store, write to it and re-open it", func(t *testing.T) {
		fpath := G.Path(path.Join(t.TempDir(), "db.json"))
		ctx := newStoreTestContext(fpath)

		store, err := OpenOrCreateStore(ctx, fpath)
		assert.NoError(t, err)

		store.Set(ctx, "a", 1.0)
		assert.NoError(t, store.persist())

		store, err = OpenOrCreateStore(ctx, fpath)

		assert.NoError(t, err)
		assert.NotNil(t, store)

		v, _ := store.Get(ctx, "a")

		assert.Equal(t, 1.0, v)
	})

}

func TestStorePersistence(t *testing.T) {
	fpath := G.Path(path.Join(t.TempDir(), "db.json"))
	ctx := newStoreTestContext(fpath)

	store, err := OpenOrCreateStore(ctx, fpath)
	assert.NoError(t, err)
	store.Set(ctx, "a", 1.0)

	statBeforePersistence, _ := os.Stat(string(fpath))
	time.Sleep(2 * KV_STORE_PERSISTENCE_INTERVAL)

	statAfterPersistence, _ := os.Stat(string(fpath))

	assert.Greater(t, statAfterPersistence.Size(), statBeforePersistence.Size())
}
