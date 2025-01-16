package protocol

import "fmt"

const (
    OpInitialize      = "initialize"
    OpCreateDownloader = "create_downloader"
    OpDownload        = "download"
    OpCleanup         = "cleanup"
    OpValidate        = "validate"
)

var (
    ErrInvalidURL     = fmt.Errorf("invalid URL")
    ErrUnsupportedURL = fmt.Errorf("unsupported URL for this protocol")
    ErrDownloadFailed = fmt.Errorf("download failed")
)

type ProtocolError struct {
    Protocol  string
    Operation string
    URL       string
    Err       error
}

func (e *ProtocolError) Error() string {
    return fmt.Sprintf("%s protocol error during %s for %s: %v",
        e.Protocol, e.Operation, e.URL, e.Err)
}

func (e *ProtocolError) Unwrap() error {
    return e.Err
}

func NewProtocolError(protocol, operation string, err error) error {
    if err == nil {
        return nil
    }
    return &ProtocolError{
        Protocol:  protocol,
        Operation: operation,
        Err:       err,
    }
}
