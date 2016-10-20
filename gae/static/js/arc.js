eye = angular.module('eye', ['ngRoute']).
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

function homeController($scope, $http) {
    $scope.recent = [];
    $scope.base = "https://storage.cloud.google.com/scenic-arc.appspot.com/basement";
    $http.get("//scenic-arc.appspot.com/api/recentImages").success(function(data) {
        $scope.recent = data;
    });

    $scope.close = function() {
        $scope.videosrc = "";
        document.getElementById("player").innerHTML = "";
    };

    $scope.play = function(which) {
        var url = $scope.base + "/" + which.fn + ".mp4";
        $scope.videosrc = url;
        var video = document.getElementById("player");
        video.innerHTML = "<source src=\""+url+"\" type=\"video/mp4\">No Support for html5 videos.</source>";
        video.load();
        video.play();
    };
}

eye.controller('IndexCtrl', ['$scope', '$http', homeController]);
