package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckPosition(t *testing.T) {
	tests := []struct {
		name             string
		x                int
		y                int
		mockResponse     string
		expectedHasStone bool
		expectedPlayer   string
		shouldError      bool
	}{
		{
			name:             "空位置",
			x:                3,
			y:                15,
			mockResponse:     `{"success": true, "has_stone": false, "player": null}`,
			expectedHasStone: false,
			expectedPlayer:   "",
			shouldError:      false,
		},
		{
			name:             "黑棋位置",
			x:                3,
			y:                15,
			mockResponse:     `{"success": true, "has_stone": true, "player": "B"}`,
			expectedHasStone: true,
			expectedPlayer:   "B",
			shouldError:      false,
		},
		{
			name:             "白棋位置",
			x:                15,
			y:                3,
			mockResponse:     `{"success": true, "has_stone": true, "player": "W"}`,
			expectedHasStone: true,
			expectedPlayer:   "W",
			shouldError:      false,
		},
		{
			name:             "服务器错误",
			x:                3,
			y:                15,
			mockResponse:     `{"success": false, "error": "internal error"}`,
			expectedHasStone: false,
			expectedPlayer:   "",
			shouldError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/api/check-position") {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(tt.mockResponse))
				}
			}))
			defer server.Close()

			originalURL := KATRAIN_URL
			KATRAIN_URL = server.URL
			defer func() { KATRAIN_URL = originalURL }()

			hasStone, player, err := checkPosition(tt.x, tt.y)

			if tt.shouldError {
				if err == nil {
					t.Errorf("checkPosition(%d, %d) expected error, got nil", tt.x, tt.y)
				}
				return
			}

			if err != nil {
				t.Errorf("checkPosition(%d, %d) unexpected error: %v", tt.x, tt.y, err)
				return
			}

			if hasStone != tt.expectedHasStone {
				t.Errorf("checkPosition(%d, %d) hasStone = %v, want %v", tt.x, tt.y, hasStone, tt.expectedHasStone)
			}

			if player != tt.expectedPlayer {
				t.Errorf("checkPosition(%d, %d) player = %s, want %s", tt.x, tt.y, player, tt.expectedPlayer)
			}
		})
	}
}

func TestMakeMove(t *testing.T) {
	tests := []struct {
		name         string
		x            int
		y            int
		player       string
		mockResponse string
		shouldError  bool
	}{
		{
			name:         "成功落子",
			x:            3,
			y:            15,
			player:       "B",
			mockResponse: `{"success": true}`,
			shouldError:  false,
		},
		{
			name:         "位置已有棋子",
			x:            3,
			y:            15,
			player:       "B",
			mockResponse: `{"success": false, "error": "该坐标已有棋子"}`,
			shouldError:  true,
		},
		{
			name:         "无效玩家",
			x:            3,
			y:            15,
			player:       "X",
			mockResponse: `{"success": false, "error": "玩家颜色必须是 B 或 W"}`,
			shouldError:  true,
		},
		{
			name:         "服务器错误",
			x:            3,
			y:            15,
			player:       "B",
			mockResponse: `{"success": false, "error": "internal server error"}`,
			shouldError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/api/make-move") {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(tt.mockResponse))
				}
			}))
			defer server.Close()

			originalURL := KATRAIN_URL
			KATRAIN_URL = server.URL
			defer func() { KATRAIN_URL = originalURL }()

			err := makeMove(tt.x, tt.y, tt.player)

			if tt.shouldError {
				if err == nil {
					t.Errorf("makeMove(%d, %d, %s) expected error, got nil", tt.x, tt.y, tt.player)
				}
				return
			}

			if err != nil {
				t.Errorf("makeMove(%d, %d, %s) unexpected error: %v", tt.x, tt.y, tt.player, err)
			}
		})
	}
}

func TestGetLastMove(t *testing.T) {
	tests := []struct {
		name            string
		mockResponse    string
		expectedX       int
		expectedY       int
		expectedPlayer  string
		expectedMoveNum int
		shouldError     bool
	}{
		{
			name:            "有最后一手",
			mockResponse:    `{"success": true, "move_number": 5, "last_move": {"player": "W", "move_number": 5, "coords": [3, 15]}}`,
			expectedX:       3,
			expectedY:       15,
			expectedPlayer:  "W",
			expectedMoveNum: 5,
			shouldError:     false,
		},
		{
			name:            "无落子",
			mockResponse:    `{"success": true, "move_number": 0, "last_move": null}`,
			expectedX:       0,
			expectedY:       0,
			expectedPlayer:  "",
			expectedMoveNum: 0,
			shouldError:     false,
		},
		{
			name:            "服务器错误",
			mockResponse:    `{"success": false, "error": "cannot get board info"}`,
			expectedX:       0,
			expectedY:       0,
			expectedPlayer:  "",
			expectedMoveNum: 0,
			shouldError:     true,
		},
		{
			name:            "黑棋落子",
			mockResponse:    `{"success": true, "move_number": 1, "last_move": {"player": "B", "move_number": 1, "coords": [9, 9]}}`,
			expectedX:       9,
			expectedY:       9,
			expectedPlayer:  "B",
			expectedMoveNum: 1,
			shouldError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/api/last-move") {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(tt.mockResponse))
				}
			}))
			defer server.Close()

			originalURL := KATRAIN_URL
			KATRAIN_URL = server.URL
			defer func() { KATRAIN_URL = originalURL }()

			x, y, player, moveNum, err := getLastMove()

			if tt.shouldError {
				if err == nil {
					t.Errorf("getLastMove() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("getLastMove() unexpected error: %v", err)
				return
			}

			if x != tt.expectedX {
				t.Errorf("getLastMove() x = %d, want %d", x, tt.expectedX)
			}

			if y != tt.expectedY {
				t.Errorf("getLastMove() y = %d, want %d", y, tt.expectedY)
			}

			if player != tt.expectedPlayer {
				t.Errorf("getLastMove() player = %s, want %s", player, tt.expectedPlayer)
			}

			if moveNum != tt.expectedMoveNum {
				t.Errorf("getLastMove() moveNum = %d, want %d", moveNum, tt.expectedMoveNum)
			}
		})
	}
}
