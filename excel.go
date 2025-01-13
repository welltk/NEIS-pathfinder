package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ncruces/zenity"
	"github.com/xuri/excelize/v2"
)

type ExcelProcessorImpl struct {
	cache map[string]CacheEntry
}

type CacheEntry struct {
	Coordinates  Coordinates
	AreaCode     string
	MinFare      int
	CarDistance  int
	WalkDistance int
}

type Coordinates struct {
	X, Y float64
}

func NewExcelProcessorImpl() *ExcelProcessorImpl {
	return &ExcelProcessorImpl{
		cache: make(map[string]CacheEntry),
	}
}

func (ep *ExcelProcessorImpl) ProcessExcelFile(ctx context.Context, config Config, api TMapAPI, updater StatusUpdater) error {
	f, err := excelize.OpenFile(config.ExcelFilePath)
	if err != nil {
		log.Printf("엑셀 파일 열기 실패: %v", err)
		return fmt.Errorf("엑셀 파일 열기 실패: %v", err)
	}
	defer f.Close()

	sheetName := f.GetSheetName(0)
	rows, err := f.GetRows(sheetName)
	if err != nil {
		log.Printf("시트 데이터 읽기 실패: %v", err)
		return fmt.Errorf("시트 데이터 읽기 실패: %v", err)
	}

	log.Printf("총 %d개의 행을 처리합니다.", len(rows)-2)

	newHeaders := map[string]string{
		"N1": "출발지 행정구역",
		"O1": "도착지 행정구역",
		"P1": "최소 운임비(대중교통)",
		"Q1": "왕복 운임비(대중교통)",
		"R1": "최소 거리(자동차)",
		"S1": "최소 거리(도보)",
		"T1": "왕복 2km 이상 여부(자동차)",
		"U1": "왕복 2km 이상 여부(도보)",
	}

	// 헤더 추가 및 스타일 적용
	applyHeaderStyles(f, sheetName, newHeaders)

	// 열 너비 설정
	columnWidths := map[string]float64{
		"A": 10, "B": 10, "C": 10, "D": 20, "E": 24, "F": 30,
		"N": 20, "O": 20, "P": 26, "Q": 26, "R": 26, "S": 22, "T": 31, "U": 30,
	}
	for col, width := range columnWidths {
		f.SetColWidth(sheetName, col, col, width)
	}

	borderStyle, err := f.NewStyle(&excelize.Style{
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
		},
	})
	if err != nil {
		log.Printf("테두리 스타일 생성 실패: %v", err)
		return fmt.Errorf("테두리 스타일 생성 실패: %v", err)
	}

	// 1-2행 병합 로직 추가
	for col := 'A'; col <= 'U'; col++ {
		cellStart := fmt.Sprintf("%c1", col)
		cellEnd := fmt.Sprintf("%c2", col)
		if err := f.MergeCell(sheetName, cellStart, cellEnd); err != nil {
			log.Printf("셀 병합 실패 (%s-%s): %v", cellStart, cellEnd, err)
		}
	}

	for i := 2; i < len(rows); i++ { // 3행부터 시작 (인덱스는 2부터)
		select {
		case <-ctx.Done():
			log.Println("작업이 취소되었습니다.")
			return nil
		default:
			status := fmt.Sprintf("처리 중: %d / %d", i-1, len(rows)-2)
			log.Println(status)
			updater.UpdateStatus(status)

			if len(rows[i]) < 6 {
				log.Printf("행 %d: 데이터 부족, 건너뜁니다", i+1)
				updater.UpdateStatus(fmt.Sprintf("행 %d: 데이터 부족, 건너뜁니다", i+1))
				continue
			}

			//출장 목적 (여비부지급 여부 확인)
			if len(rows[i]) > 5 && strings.Contains(rows[i][5], "여비부지급") {
				log.Printf("행 %d: 여비부지급 항목, 건너뜁니다", i+1)
				updater.UpdateStatus(fmt.Sprintf("행 %d: 여비부지급 항목, 건너뜁니다", i+1))
				continue
			}

			// A~M열 데이터 복사
			for col := 0; col < 13; col++ {
				if col < len(rows[i]) {
					cellValue := rows[i][col]
					f.SetCellValue(sheetName, fmt.Sprintf("%s%d", string(rune('A'+col)), i+1), cellValue)
				}
			}

			startName := processPlaceName(rows[i][2]) // 신청당시부서
			endName := processPlaceName(rows[i][4])   // 출장지

			log.Printf("출발지: %s, 도착지: %s", startName, endName)

			startCoords, startAreaCode, err := ep.getCoordinatesAndAreaCode(api, startName)
			if err != nil {
				log.Printf("행 %d: 출발지 좌표 및 행정구역 검색 실패: %v", i+1, err)
				updater.UpdateStatus(fmt.Sprintf("행 %d: 출발지 좌표 및 행정구역 검색 실패: %v", i+1, err))
				continue
			}

			endCoords, endAreaCode, err := ep.getCoordinatesAndAreaCode(api, endName)
			if err != nil {
				log.Printf("행 %d: 도착지 좌표 및 행정구역 검색 실패: %v", i+1, err)
				updater.UpdateStatus(fmt.Sprintf("행 %d: 도착지 좌표 및 행정구역 검색 실패: %v", i+1, err))
				continue
			}

			f.SetCellValue(sheetName, fmt.Sprintf("N%d", i+1), startAreaCode)
			f.SetCellValue(sheetName, fmt.Sprintf("O%d", i+1), endAreaCode)

			cacheKey := fmt.Sprintf("%s-%s", startName, endName)
			entry, exists := ep.cache[cacheKey]
			if !exists {
				minFare, err := api.GetMinimumTransitFare(startCoords.X, startCoords.Y, endCoords.X, endCoords.Y)
				if err != nil {
					log.Printf("행 %d: 대중교통 요금 계산 실패: %v", i+1, err)
					updater.UpdateStatus(fmt.Sprintf("행 %d: 대중교통 요금 계산 실패: %v", i+1, err))
					minFare = 0
				} else {
					log.Printf("행 %d: 최소 운임비: %d", i+1, minFare)
				}

				carDistance, err := api.GetCarRouteDistance(startCoords.X, startCoords.Y, endCoords.X, endCoords.Y)
				if err != nil {
					log.Printf("행 %d: 자동차 경로 계산 실패: %v", i+1, err)
					updater.UpdateStatus(fmt.Sprintf("행 %d: 자동차 경로 계산 실패: %v", i+1, err))
					carDistance = 0 // 오류 시 0으로 설정
				}

				walkDistance, err := api.GetPedestrianRouteDistance(startCoords.X, startCoords.Y, endCoords.X, endCoords.Y)
				if err != nil {
					log.Printf("행 %d: 도보 경로 계산 실패: %v", i+1, err)
					updater.UpdateStatus(fmt.Sprintf("행 %d: 도보 경로 계산 실패: %v", i+1, err))
					walkDistance = 0 // 오류 시 0으로 설정
				}

				entry = CacheEntry{
					MinFare:      minFare,
					CarDistance:  carDistance,
					WalkDistance: walkDistance,
				}
				ep.cache[cacheKey] = entry
			} else {
				log.Printf("캐시에서 데이터를 가져왔습니다: %s", cacheKey)
			}

			// 결과 기록 및 스타일 적용
			for col := 'N'; col <= 'U'; col++ {
				cell := fmt.Sprintf("%c%d", col, i+1)
				f.SetCellStyle(sheetName, cell, cell, borderStyle)
			}

			recordResults(f, sheetName, i, entry.MinFare, entry.CarDistance, entry.WalkDistance)

			log.Printf("행 %d 처리 완료", i+1)
			time.Sleep(time.Second) // API 호출 간 간격
		}
	}

	// 결과 파일 저장
	newFilePath := getNewFilePath(f, config.ExcelFilePath)
	if err := f.SaveAs(newFilePath); err != nil {
		log.Printf("결과 파일 저장 실패: %v", err)
		zenity.Error(fmt.Sprintf("새 파일 저장 중 오류 발생: %v", err))
		return fmt.Errorf("결과 파일 저장 실패: %v", err)
	}

	log.Printf("처리 완료. 결과 파일: %s", newFilePath)
	updater.UpdateStatus(fmt.Sprintf("처리 완료. 결과 파일: %s", newFilePath))

	// 파일 생성 완료 알림 및 파일 열기 옵션
	if err := zenity.Question("작업이 완료되었습니다. 결과 파일을 여시겠습니까?"); err == nil {
		if err := exec.Command("cmd", "/C", "start", "", newFilePath).Start(); err != nil {
			log.Printf("파일 열기 오류: %v", err)
		}
	}

	return nil
}

func (ep *ExcelProcessorImpl) getCoordinatesAndAreaCode(api TMapAPI, placeName string) (Coordinates, string, error) {
	if entry, exists := ep.cache[placeName]; exists {
		return entry.Coordinates, entry.AreaCode, nil
	}

	x, y, areaCode, err := api.GetCoordinatesByPlaceName(placeName)
	if err != nil {
		log.Printf("API 호출 실패: placeName=%s, error=%v", placeName, err)
		return Coordinates{}, "", fmt.Errorf("API 호출 실패: %w", err)
	}

	if ep.cache == nil {
		ep.cache = make(map[string]CacheEntry)
	}

	coords := Coordinates{X: x, Y: y}
	ep.cache[placeName] = CacheEntry{
		Coordinates: coords,
		AreaCode:    areaCode,
	}

	log.Printf("캐시에 저장: placeName=%s, coords=%v, areaCode=%s", placeName, coords, areaCode)
	return coords, areaCode, nil
}

func applyHeaderStyles(f *excelize.File, sheetName string, headers map[string]string) {
	headerColors := map[string]string{
		"N": "E6E6FA", "O": "E6E6FA", // 행정구역 (연한 보라색)
		"P": "FFD580", "Q": "FFD580", // 대중교통 (노란색)
		"R": "99CCFF", "T": "99CCFF", // 자동차 (파란색)
		"S": "66CC66", "U": "66CC66", // 도보 (초록색)
	}
	grayColor := "D3D3D3"

	for cell, value := range headers {
		f.SetCellValue(sheetName, cell, value)

		style, err := f.NewStyle(&excelize.Style{
			Font: &excelize.Font{
				Family: "Malgun Gothic",
				Size:   12,
				Bold:   true,
				Color:  "000000",
			},
			Fill: excelize.Fill{
				Type:    "pattern",
				Color:   []string{headerColors[string(cell[0])]},
				Pattern: 1,
			},
			Alignment: &excelize.Alignment{
				Horizontal: "center",
				Vertical:   "center",
			},
			Border: []excelize.Border{
				{Type: "left", Color: "000000", Style: 1},
				{Type: "top", Color: "000000", Style: 1},
				{Type: "bottom", Color: "000000", Style: 1},
				{Type: "right", Color: "000000", Style: 1},
			},
		})
		if err != nil {
			log.Printf("헤더 스타일 생성 중 오류 발생 (%s 열): %v\n", cell, err)
			continue
		}

		f.SetCellStyle(sheetName, cell, cell, style)
	}

	// A~M 열 스타일 적용
	for col := 'A'; col <= 'M'; col++ {
		cell := fmt.Sprintf("%c1", col)
		style, _ := f.NewStyle(&excelize.Style{
			Font: &excelize.Font{
				Family: "Malgun Gothic",
				Size:   12,
				Bold:   true,
				Color:  "000000",
			},
			Fill: excelize.Fill{
				Type:    "pattern",
				Color:   []string{grayColor},
				Pattern: 1,
			},
			Alignment: &excelize.Alignment{
				Horizontal: "center",
				Vertical:   "center",
			},
			Border: []excelize.Border{
				{Type: "left", Color: "000000", Style: 1},
				{Type: "top", Color: "000000", Style: 1},
				{Type: "bottom", Color: "000000", Style: 1},
				{Type: "right", Color: "000000", Style: 1},
			},
		})
		f.SetCellStyle(sheetName, cell, cell, style)
	}
}

func recordResults(f *excelize.File, sheetName string, row int, minFare, carDistance, walkDistance int) {
	// 최소 운임비(대중교통)
	formattedMinFare := fmt.Sprintf("%s원", formatNumberWithComma(minFare))
	f.SetCellValue(sheetName, fmt.Sprintf("P%d", row+1), formattedMinFare)

	// 왕복 운임비(대중교통)
	formattedRoundTripFare := fmt.Sprintf("%s원", formatNumberWithComma(minFare*2))
	f.SetCellValue(sheetName, fmt.Sprintf("Q%d", row+1), formattedRoundTripFare)

	// 최소 거리(자동차)
	carDistanceKm := float64(carDistance) / 1000
	f.SetCellValue(sheetName, fmt.Sprintf("R%d", row+1), fmt.Sprintf("%.2fkm", carDistanceKm))

	// 최소 거리(도보)
	if walkDistance == -1 {
		f.SetCellValue(sheetName, fmt.Sprintf("S%d", row+1), "직선거리 일정 이상 초과")
	} else {
		walkDistanceKm := float64(walkDistance) / 1000
		f.SetCellValue(sheetName, fmt.Sprintf("S%d", row+1), fmt.Sprintf("%.2fkm", walkDistanceKm))
	}

	// 왕복 2km 이상 여부(자동차)
	if carDistanceKm*2 >= 2.0 {
		f.SetCellValue(sheetName, fmt.Sprintf("T%d", row+1), "Y")
	} else {
		f.SetCellValue(sheetName, fmt.Sprintf("T%d", row+1), "N")
	}

	// 왕복 2km 이상 여부(도보)
	if walkDistance == -1 || float64(walkDistance)/1000*2 >= 2.0 {
		f.SetCellValue(sheetName, fmt.Sprintf("U%d", row+1), "Y")
	} else {
		f.SetCellValue(sheetName, fmt.Sprintf("U%d", row+1), "N")
	}
}

func getTripPeriod(f *excelize.File) string {
	sheetName := f.GetSheetName(0)
	tripPeriod, err := f.GetCellValue(sheetName, "H3") // 출장 기간
	if err != nil || len(tripPeriod) < 7 {
		return "No_tripPeriod"
	}
	return tripPeriod[:7] // 앞 7글자만 추출
}

func getNewFilePath(f *excelize.File, originalPath string) string {
	dir := filepath.Dir(originalPath)
	filename := filepath.Base(originalPath)
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)

	tripPeriod := getTripPeriod(f)

	// 새 파일 이름 생성: 원본이름_tripperiod_API.xlsx
	newFileName := fmt.Sprintf("%s_%s_API.xlsx", name, tripPeriod)

	return filepath.Join(dir, newFileName)
}

func processPlaceName(name string) string {
	// 괄호 제거
	name = removeBrackets(name)

	// "및" 또는 "," 로 분리된 경우 처리
	if strings.Contains(name, " 및 ") || strings.Contains(name, ",") {
		options := strings.Split(strings.ReplaceAll(name, " 및 ", ","), ",")
		for i, opt := range options {
			options[i] = strings.TrimSpace(opt)
		}
		selected, err := zenity.List(
			"장소를 선택하세요",
			options,
			zenity.Title("장소 선택"),
		)
		if err != nil {
			// 오류 발생 시 또는 사용자가 취소한 경우 첫 번째 옵션 선택
			log.Printf("장소 선택 중 오류 발생: %v", err)
			return options[0]
		}
		return selected
	}

	return strings.TrimSpace(name)
}

func removeBrackets(name string) string {
	for {
		start := strings.Index(name, "(")
		if start == -1 {
			break
		}
		end := strings.Index(name[start:], ")") + start
		if end == -1 {
			break
		}
		name = name[:start] + name[end+1:]
	}
	return name
}

func formatNumberWithComma(n int) string {
	formattedNumber := strconv.Itoa(n)
	if len(formattedNumber) > 3 {
		for i := len(formattedNumber) - 3; i > 0; i -= 3 {
			formattedNumber = formattedNumber[:i] + "," + formattedNumber[i:]
		}
	}
	return formattedNumber
}
