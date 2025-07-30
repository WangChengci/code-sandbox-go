package file

import (
	filepb "code-sandbox-go/api/protos/file"
	"context"
	"fmt"
	"os"
	"path/filepath"
)

//	type SaveFileRequest struct {
//		state         protoimpl.MessageState `protogen:"open.v1"`
//		Content       string                 `protobuf:"bytes,1,opt,name=content,proto3" json:"content,omitempty"`
//		FileName      string                 `protobuf:"bytes,2,opt,name=fileName,proto3" json:"fileName,omitempty"`
//		unknownFields protoimpl.UnknownFields
//		sizeCache     protoimpl.SizeCache
//	}
type FileService struct {
	filepb.UnimplementedFileServiceServer
	baseDir string
}

func NewFileService(baseDir string) *FileService {
	// 确保基础目录存在
	os.MkdirAll(baseDir, 0755)
	return &FileService{
		baseDir: baseDir,
	}
}

func (fs *FileService) SaveFile(ctx context.Context, req *filepb.SaveFileRequest) (*filepb.SaveFileResponse, error) {
	content := req.Content
	fileName := req.FileName

	filePath := filepath.Join(fs.baseDir, fileName)
	// 确保文件目录存在，没有则创建
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &filepb.SaveFileResponse{
			Success: false,
		}, fmt.Errorf("创建目录失败: %v", err)
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return &filepb.SaveFileResponse{
			Success: false,
		}, fmt.Errorf("获取绝对路径失败: %w", err)
	}

	relPath, err := filepath.Rel(fs.baseDir, absPath)
	if err != nil {
		return &filepb.SaveFileResponse{
			Success: false,
		}, fmt.Errorf("计算相对路径失败: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return &filepb.SaveFileResponse{
			Success: false,
		}, fmt.Errorf("写入文件失败: %v", err)
	}

	// 获取文件信息
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return &filepb.SaveFileResponse{
			Success: false,
		}, fmt.Errorf("获取文件信息失败: %v", err)
	}

	file := &filepb.File{
		FileName:     fileName,
		AbsolutePath: absPath,
		RelativePath: relPath,
		SizeBytes:    fileInfo.Size(),
		CreatedAt:    fileInfo.ModTime().Unix(),
	}

	return &filepb.SaveFileResponse{
		Success: true,
		File:    file,
	}, nil
}

func (fs *FileService) DeleteFile(ctx context.Context, req *filepb.DeleteFileRequest) (*filepb.DeleteFileResponse, error) {
	filePath := req.File.AbsolutePath
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return &filepb.DeleteFileResponse{
				Success: true,
			}, nil
		}
		return &filepb.DeleteFileResponse{
			Success: false,
		}, fmt.Errorf("删除文件失败: %v", err)
	}

	return &filepb.DeleteFileResponse{
		Success: true,
	}, nil
}

func (fs *FileService) DeleteDir(ctx context.Context, req *filepb.DeleteFileRequest) (*filepb.DeleteFileResponse, error) {
	dirPath := filepath.Dir(req.File.AbsolutePath)
	if err := os.RemoveAll(dirPath); err != nil {
		if os.IsNotExist(err) {
			return &filepb.DeleteFileResponse{
				Success: true,
			}, nil
		}
		return &filepb.DeleteFileResponse{
			Success: false,
		}, fmt.Errorf("删除目录失败: %v", err)
	}

	return &filepb.DeleteFileResponse{
		Success: true,
	}, nil
}

// GetFile 获取文件内容
func (fs *FileService) GetFile(ctx context.Context, req *filepb.GetFileRequest) (*filepb.GetFileResponse, error) {
	filePath := req.File.AbsolutePath
	content, err := os.ReadFile(filePath)
	if err != nil {
		return &filepb.GetFileResponse{
			Success: false,
		}, fmt.Errorf("读取文件失败: %v", err)
	}

	return &filepb.GetFileResponse{
		Success: true,
		Content: string(content),
	}, nil
}
