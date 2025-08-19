package types

import (
	"io"
)

type Serializable interface {
	Serialize(w io.Writer) error
	Deserialize(r io.Reader) error
}

type Chunk struct {
	Id       int    `json:"id"`
	Offset   int    `json:"offset"`
	Size     int    `json:"size"`
	Data     []byte `json:"data,omitempty"`
	Checksum string `json:"checksum,omitempty"`
}

type Request struct {
	Type     string `json:"type"`
	Resource string `json:"resource"`
}

// Command porte une instruction à exécuter côté secondaire pour lire
// un morceau de fichier et l'envoyer au client avec l'offset de stream attendu.
type Command struct {
	Resource     string `json:"resource"`      // chemin vers la ressource
	ChunkID      int    `json:"chunk_id"`      // identifiant du chunk (0-based)
	FileOffset   int64  `json:"file_offset"`   // offset dans le fichier
	ReadSize     int    `json:"read_size"`     // taille à lire
	StreamOffset int64  `json:"stream_offset"` // offset à définir sur le stream
}

// FileInfo regroupe les méta-données de fichier. Non optionnel en tant que type.
type FileInfo struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	NumChunks int    `json:"num_chunks"`
	ChunkSize int    `json:"chunk_size"`
}

// Response unifiée pour une requête: succès/échec. Les méta-données de fichier
// sont regroupées dans FileInfo, rendues optionnelles en tant que bloc entier.
type Response struct {
	Success bool      `json:"success"`
	Error   string    `json:"error,omitempty"`
	File    *FileInfo `json:"file,omitempty"`
}
