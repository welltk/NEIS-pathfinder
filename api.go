package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
)

const (
	tmapAPIBaseURL         = "https://apis.openapi.sk.com"
	tmapPlaceSearchURL     = tmapAPIBaseURL + "/tmap/pois?version=1"
	tmapCarRouteURL        = tmapAPIBaseURL + "/tmap/routes?version=1"
	tmapPedestrianRouteURL = tmapAPIBaseURL + "/tmap/routes/pedestrian?version=1&format=json"
	tmapTransitRouteURL    = tmapAPIBaseURL + "/transit/routes?version=1&format=json"
)

type TMapAPIImpl struct {
	AppKey string
}

func (api *TMapAPIImpl) GetCoordinatesByPlaceName(placeName string) (float64, float64, string, error) { //좌표 찾기
	encodedPlaceName := url.QueryEscape(placeName)
	urlStr := fmt.Sprintf("%s&searchKeyword=%s&appKey=%s", tmapPlaceSearchURL, encodedPlaceName, api.AppKey)

	resp, err := http.Get(urlStr)
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, "", err
	}

	var placeResponse struct {
		SearchPoiInfo struct {
			Pois struct {
				Poi []struct {
					FrontLat       string `json:"frontLat"`
					FrontLon       string `json:"frontLon"`
					UpperAddrName  string `json:"upperAddrName"`
					MiddleAddrName string `json:"middleAddrName"`
				} `json:"poi"`
			} `json:"pois"`
		} `json:"searchPoiInfo"`
	}

	if err := json.Unmarshal(body, &placeResponse); err != nil {
		return 0, 0, "", err
	}

	if len(placeResponse.SearchPoiInfo.Pois.Poi) == 0 {
		return 0, 0, "", fmt.Errorf("장소를 찾을 수 없습니다: %s", placeName)
	}

	poi := placeResponse.SearchPoiInfo.Pois.Poi[0]
	lat, _ := strconv.ParseFloat(poi.FrontLat, 64)
	lon, _ := strconv.ParseFloat(poi.FrontLon, 64)
	areaCode := poi.UpperAddrName + " " + poi.MiddleAddrName

	return lon, lat, areaCode, nil
}

func (api *TMapAPIImpl) GetCarRouteDistance(startX, startY, endX, endY float64) (int, error) { //자동차 거리 찾기
	reqData := struct {
		StartX float64 `json:"startX"`
		StartY float64 `json:"startY"`
		EndX   float64 `json:"endX"`
		EndY   float64 `json:"endY"`
	}{
		StartX: startX,
		StartY: startY,
		EndX:   endX,
		EndY:   endY,
	}

	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest("POST", tmapCarRouteURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("appKey", api.AppKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var routeResponse struct {
		Features []struct {
			Properties struct {
				TotalDistance int `json:"totalDistance"`
			} `json:"properties"`
		} `json:"features"`
	}

	if err := json.Unmarshal(body, &routeResponse); err != nil {
		return 0, err
	}

	if len(routeResponse.Features) == 0 {
		return 0, fmt.Errorf("경로를 찾을 수 없습니다")
	}

	return routeResponse.Features[0].Properties.TotalDistance, nil
}

func (api *TMapAPIImpl) GetPedestrianRouteDistance(startX, startY, endX, endY float64) (int, error) { //도보 거리 찾기
	reqData := struct {
		StartX       float64 `json:"startX"`
		StartY       float64 `json:"startY"`
		EndX         float64 `json:"endX"`
		EndY         float64 `json:"endY"`
		StartName    string  `json:"startName"`
		EndName      string  `json:"endName"`
		SearchOption int     `json:"searchOption"`
	}{
		StartX:       startX,
		StartY:       startY,
		EndX:         endX,
		EndY:         endY,
		StartName:    "출발",
		EndName:      "도착",
		SearchOption: 0,
	}

	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest("POST", tmapPedestrianRouteURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("appKey", api.AppKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var routeResponse struct {
		Features []struct {
			Properties struct {
				TotalDistance int `json:"totalDistance"`
			} `json:"properties"`
		} `json:"features"`
	}

	if err := json.Unmarshal(body, &routeResponse); err != nil {
		return 0, err
	}

	if len(routeResponse.Features) == 0 {
		return -1, nil // 직선거리 일정 이상 초과 또는 경로를 찾을 수 없는 경우
	}

	return routeResponse.Features[0].Properties.TotalDistance, nil
}

func (api *TMapAPIImpl) GetMinimumTransitFare(startX, startY, endX, endY float64) (int, error) { //최소 대중교통 운임 찾기 (대중교통 API Limit은 매우 낮으니 주의!)
	reqData := struct {
		StartX float64 `json:"startX"`
		StartY float64 `json:"startY"`
		EndX   float64 `json:"endX"`
		EndY   float64 `json:"endY"`
		Format string  `json:"format"`
	}{
		StartX: startX,
		StartY: startY,
		EndX:   endX,
		EndY:   endY,
		Format: "json",
	}

	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest("POST", tmapTransitRouteURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("appKey", api.AppKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("API 요청 실패: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("응답 본문 읽기 실패: %w", err)
	}

	log.Printf("API 응답: %s", string(body))

	var transitResponse struct {
		MetaData struct {
			Plan struct {
				Itineraries []struct {
					Fare struct {
						Regular struct {
							TotalFare int `json:"totalFare"`
						} `json:"regular"`
					} `json:"fare"`
					Legs []struct {
						RoutePayment int `json:"routePayment"`
					} `json:"legs"`
				} `json:"itineraries"`
			} `json:"plan"`
		} `json:"metaData"`
	}

	if err := json.Unmarshal(body, &transitResponse); err != nil {
		return 0, fmt.Errorf("JSON 파싱 실패: %w", err)
	}

	minFare := math.MaxInt32
	for _, itinerary := range transitResponse.MetaData.Plan.Itineraries {
		totalFare := itinerary.Fare.Regular.TotalFare
		if totalFare > 0 && totalFare < minFare {
			minFare = totalFare
		}

		for _, leg := range itinerary.Legs {
			routePayment := leg.RoutePayment
			if routePayment > 0 && routePayment < minFare {
				minFare = routePayment
			}
		}
	}

	if minFare == math.MaxInt32 {
		return 0, fmt.Errorf("유효한 요금 정보를 찾을 수 없습니다")
	}

	log.Printf("최소 운임비: %d", minFare)
	return minFare, nil
}
