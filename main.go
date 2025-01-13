package main

import (
	"context"
	"fmt"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/ncruces/zenity"
)

type my_theme struct {
	fontSize float32
}

func (m *my_theme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameText {
		return m.fontSize
	}
	return theme.DefaultTheme().Size(name)
}

func (m *my_theme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(name, variant)
}

func (m *my_theme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (m *my_theme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

type Config struct {
	AppKey        string
	ExcelFilePath string
}

type StatusUpdater interface {
	UpdateStatus(status string)
}

type TMapAPI interface {
	GetCoordinatesByPlaceName(placeName string) (float64, float64, string, error)
	GetCarRouteDistance(startX, startY, endX, endY float64) (int, error)
	GetPedestrianRouteDistance(startX, startY, endX, endY float64) (int, error)
	GetMinimumTransitFare(startX, startY, endX, endY float64) (int, error)
}

type ExcelProcessor interface {
	ProcessExcelFile(ctx context.Context, config Config, api TMapAPI, updater StatusUpdater) error
}

func main() {
	a := app.New()
	w := a.NewWindow("출장 운임 정보 수집 프로그램 Demo (API)")
	currentFontSize := float32(20.0)
	customTheme := &my_theme{fontSize: currentFontSize}
	a.Settings().SetTheme(customTheme)

	config := &Config{}
	statusChan := make(chan string, 1)
	var searching bool
	var cancelFunc context.CancelFunc
	var cancelMutex sync.Mutex

	statusLabel := widget.NewLabel("대기 중...")
	go func() {
		for status := range statusChan {
			statusLabel.SetText(status)
		}
	}()

	appKeyEntry := widget.NewEntry()
	appKeyEntry.SetPlaceHolder("TMap API 키를 입력하세요")

	fileLoadButton := widget.NewButton("파일 불러오기", func() {
		loadFile(config, statusChan)
	})

	searchBtn := widget.NewButton("검색 시작", func() {
		handleSearch(config, appKeyEntry.Text, &searching, &cancelFunc, &cancelMutex, statusChan)
	})

	content := container.NewVBox(
		widget.NewLabel("※사용법※\n1. 나이스 ➜ 복무 ➜ 출장 관리 ➜ 엑셀 내려받기\n2. '파일 불러오기' 버튼을 눌러서 내려받은 파일을 불러옵니다.\n3. TMap API 키를 입력합니다. \n4. '검색 시작' 버튼을 누릅니다. \n\n※기능※\n1. 대중교통 최소 운임비 수집 \n2. 자동차 최소 이동 거리 수집\n3. 도보 최소 이동 거리 수집\n4. 출발지 및 도착지 행정구역 수집"),
		appKeyEntry,
		fileLoadButton,
		searchBtn,
		statusLabel,
	)

	w.SetContent(content)
	w.Resize(fyne.NewSize(500, 500))
	w.ShowAndRun()
}
func setupLogging() *os.File {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("로그 디렉토리 생성 실패: %v", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	logPath := filepath.Join(logDir, fmt.Sprintf("app_log_%s.txt", timestamp))

	logFile, err := os.Create(logPath)
	if err != nil {
		log.Fatalf("로그 파일 생성 실패: %v", err)
	}

	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Println("로깅 시작")
	return logFile
}

func handleSearch(config *Config, enteredAppKey string, searching *bool, cancelFunc *context.CancelFunc, cancelMutex *sync.Mutex, statusChan chan<- string) {
	cancelMutex.Lock()
	defer cancelMutex.Unlock()

	if *searching {
		if *cancelFunc != nil {
			(*cancelFunc)()
		}
		*searching = false
		statusChan <- "검색 중지됨"
		log.Println("검색 중지됨")
		return
	}

	if config.ExcelFilePath == "" {
		statusChan <- "오류: 파일을 먼저 선택해주세요"
		log.Println("오류: 파일을 먼저 선택해주세요")
		return
	}

	config.AppKey = enteredAppKey
	if config.AppKey == "" {
		statusChan <- "오류: TMap API 키를 입력해주세요"
		log.Println("오류: TMap API 키를 입력해주세요")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	*cancelFunc = cancel
	statusChan <- "검색 중..."
	log.Println("검색 시작")
	*searching = true

	go func() {
		api := &TMapAPIImpl{AppKey: config.AppKey}
		processor := &ExcelProcessorImpl{}
		updater := &StatusUpdaterImpl{statusChan: statusChan}

		err := processor.ProcessExcelFile(ctx, *config, api, updater)
		if err != nil {
			errMsg := fmt.Sprintf("오류 발생: %v", err)
			statusChan <- errMsg
			log.Println(errMsg)
		} else {
			statusChan <- "검색 완료"
			log.Println("검색 완료")
		}

		cancelMutex.Lock()
		*searching = false
		cancelMutex.Unlock()
	}()
}

func loadFile(config *Config, statusChan chan<- string) {
	filePath, err := zenity.SelectFile(
		zenity.Title("엑셀 파일을 선택하세요"),
		zenity.FileFilter{
			Name:     "엑셀 파일",
			Patterns: []string{"*.xlsx"},
		},
	)
	if err != nil {
		if err != zenity.ErrCanceled {
			statusChan <- fmt.Sprintf("파일 선택 오류: %v", err)
		}
		return
	}

	config.ExcelFilePath = filePath
	statusChan <- fmt.Sprintf("파일 선택: %s", config.ExcelFilePath)
}

type StatusUpdaterImpl struct {
	statusChan chan<- string
}

func (su *StatusUpdaterImpl) UpdateStatus(status string) {
	su.statusChan <- status
}
