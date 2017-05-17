eye = angular.module('eye', ['ngRoute', 'infinite-scroll']).
    filter('relDate', function() {
        return function(dstr) {
            return moment(dstr).fromNow();
        };
    }).
    filter('duration', function() {
        return function(d) {
            var seconds = d / 1000000000;
            var minutes = (seconds / 60).toFixed(0);
            seconds = (seconds % 60).toFixed(0);
            if (seconds.length == 1) {
                seconds = "0" + seconds;
            }
            return minutes + ":" + seconds;
        };
    }).
    filter('calDate', function() {
        return function(dstr) {
            return moment(dstr).calendar(null, {
                sameDay: '[Today] HH:mm',
                nextDay: '[Tomorrow]',
                nextWeek: 'dddd',
                lastDay: '[Yesterday] HH:mm',
                lastWeek: '[Last] dddd HH:mm',
                sameElse: 'YYYY/MM/DD HH:mm'
            });
        };
    }).
    filter('time', function() {
        return function(dstr) {
            return moment(dstr).calendar(null, {
                sameDay: 'HH:mm',
                nextDay: 'HH:mm',
                nextWeek: 'HH:mm',
                lastDay: 'HH:mm',
                lastWeek: 'HH:mm',
                sameElse: 'HH:mm'
            });
        };
    }).
    config(['$routeProvider', '$locationProvider',
            function($routeProvider, $locationProvider) {
                $locationProvider.html5Mode(true);
                $locationProvider.hashPrefix('!');

                $routeProvider.
                    when('/', {
                        templateUrl: '/static/partials/home.html',
                        controller: 'IndexCtrl'
                    }).
                    otherwise({
                        redirectTo: '/'
                    });
            }]);

function homeController($scope, $http, $interval) {
    $scope.recent = [];
    $scope.fetching = true;
    $scope.base = "https://storage.cloud.google.com/scenic-arc.appspot.com/";

    var cursor = '';
    var prev = '';
    $scope.cam = '';

    $scope.fetch = function() {
        $scope.fetching = true;
        var stuff = $scope.recent.slice();
        if (cursor) {
            console.log("fetching from", cursor);
        }
        $http.get("/api/recentImages?cam=" + encodeURIComponent($scope.cam) +
                  "&cursor=" + encodeURIComponent(cursor)).success(function(data) {
            cursor = data.cursor;
            console.log("Next cursor is", cursor);
            var current = [];
            if (stuff.length > 0) {
                var x = stuff.pop();
                prev = x.ts;
                current = x.clips;
            };
            for (var i = 0; i < data.results.length; i++) {
                var r = data.results[i];
                var day = moment(r.ts).calendar(null, {
                    sameDay: '[Today] (dddd YYYY/MM/DD)',
                    nextDay: '[Tomorrow]',
                    nextWeek: 'dddd',
                    lastDay: '[Yesterday] (dddd YYYY/MM/DD)',
                    lastWeek: '[Last] dddd (YYYY/MM/DD)',
                    sameElse: 'dddd YYYY/MM/DD'
                });
                if (day != prev) {
                    stuff.push({ts: prev, clips: current});
                    current = [];
                }
                current.push(r);
                prev = day;
            }
            stuff.push({ts: prev, clips: current});
            $scope.recent = stuff;
            $scope.fetching = false;
        });
    };
    $scope.fetch();

    /* Timestamp management for last snap images. */
    $scope.ts = new Date().getTime();
    $scope.refresh = function() {
        $scope.ts = new Date().getTime();
    };
    $scope.stoprefresh = $interval($scope.refresh, 20000);
    $scope.$on('$destroy', function() {
        $scope.stoprefresh();
    });

    $scope.snapshot = function(cam) {
        return $scope.base + cam.keyid + "/lastsnap.jpg?ts=" + $scope.ts;
    };

    $http.get("/api/cams").success(function(data) {
        $scope.cams = data;
    });

    $scope.camchange = function() {
        console.log("Cam is now", $scope.cam ? $scope.cam : 'All');
        cursor = '';
        prev = '';
        $scope.recent = [];
        $scope.fetch();
    };

    $scope.scaled = function(i) {
        var bw = 320;
        var bh = 240;
        var scale = Math.max(.1, Math.log(i.duration / 1000000000) / 8.2);
        return {w: Math.round(bw * scale), h: Math.round(bh * scale)};
    };

    $scope.close = function() {
        $scope.videosrc = "";
        document.getElementById("player").innerHTML = "";
    };

    $scope.play = function(which) {
        var url = $scope.base + which.Camera.keyid + "/" + which.fn + ".mp4";
        $scope.videosrc = url;
        var video = document.getElementById("player");
        video.innerHTML = "<source src=\""+url+"\" type=\"video/mp4\">No Support for html5 videos.</source>";
        video.load();
        video.play();
    };
}

eye.controller('IndexCtrl', ['$scope', '$http', '$interval', homeController]);
